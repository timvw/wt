package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

// Trust records which hook configurations the user has approved.
//
// Nothing runs unapproved, whatever supplied it. The alternative — naming the
// sources that need asking about and letting everything else through — makes
// the permissive answer the one you get by omission: a source nobody has
// taught the gate about, or the zero value, walks straight in and no review
// flags an absence. direnv makes the same choice for the same reason, and will
// not source even a .envrc you wrote yourself until you have said so once.
//
// A record is (scope, sha256) and BOTH must match before the commands run:
//
//   - The hash covers the whole set of commands one source contributes, so any
//     edit — a `git pull` that adds a post_create, a branch whose .wt.toml
//     differs — invalidates the approval and asks again. Approving a source
//     once and forever would let a later commit walk straight in.
//   - The scope says how widely one approval reaches. The user's own config
//     file is approved once for the machine; a repository's committed .wt.toml
//     is approved per repository, so an attacker cannot get a free pass by
//     shipping a .wt.toml byte-identical to one you already approved
//     elsewhere: `make setup` is only as safe as the Makefile next to it.
//
// The store lives in wt's own config directory, never in the repository and
// never in .git/config: a repo handed to you as a directory rather than a clone
// owns its .git/config too.

// trustScopeUser is the scope recorded for hook sets that came from the user's
// own config file. Repository scopes are always absolute paths, so a label with
// a space in it cannot collide with one.
const trustScopeUser = "user config"

// trustStoreVersion is the format of the records this build understands.
//
// Bumped to 2 when approvals moved from "this .wt.toml file's bytes" to "this
// source's commands". The two are not comparable and are deliberately not
// translated: see loadTrustStore.
const trustStoreVersion = 2

// trustRecord is a single approved hook set.
type trustRecord struct {
	Scope      string `toml:"scope"`
	Source     string `toml:"source"`
	File       string `toml:"file"`
	SHA256     string `toml:"sha256"`
	ApprovedAt string `toml:"approved_at"`
}

// trustStore is the on-disk set of approvals.
type trustStore struct {
	Version int           `toml:"version"`
	Trusted []trustRecord `toml:"trusted"`
}

// trustFilePath is the location of the trust store, or "" when configDir cannot
// name an absolute directory. Never a relative path: see errNoTrustStoreDir.
func trustFilePath() string {
	dir := configDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "trust.toml")
}

// errNoTrustStoreDir is returned when there is nowhere to keep approvals.
//
// A relative path here would be a hole rather than an inconvenience: wt runs
// from inside a repository, so "trust.toml" would resolve into the working tree
// and a cloned repo could ship approvals for its own hooks. With no home
// directory to anchor to, the honest answer is that nothing is approved.
var errNoTrustStoreDir = errors.New(
	"cannot locate a config directory to keep hook approvals in (no HOME set); " +
		"set HOME or XDG_CONFIG_HOME to an absolute path")

// loadTrustStore reads the trust store. A missing store is not an error: it
// simply means nothing has been approved yet. A malformed one *is* an error —
// unlike the config file, silently treating unreadable trust as "no trust" is
// the safe direction, but staying quiet about it would leave the user
// re-approving forever without knowing why.
func loadTrustStore() (trustStore, error) {
	var store trustStore
	path := trustFilePath()
	if path == "" {
		return trustStore{}, errNoTrustStoreDir
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		// Anything else — a permission problem, a broken symlink — is not
		// "nothing approved yet", and reading it as such would silently revoke
		// every approval the user made.
		return trustStore{}, fmt.Errorf("failed to read trust store %s: %w", path, err)
	}
	if _, err := toml.DecodeFile(path, &store); err != nil {
		return trustStore{}, fmt.Errorf("failed to read trust store %s: %w", path, err)
	}
	// A newer store is a different thing from an older one. Dropping its records
	// would be read-only if wt only read; but the next 'wt trust' writes the
	// store back, and it would go back in this version's format — quietly
	// deleting approvals a newer wt made, on a machine where both are installed.
	// Refuse instead, and leave the file alone.
	if store.Version > trustStoreVersion {
		return trustStore{}, fmt.Errorf(
			"trust store %s is version %d, and this wt reads version %d; "+
				"upgrade wt, or move that file aside to start over",
			path, store.Version, trustStoreVersion)
	}
	// Older records are dropped rather than interpreted. Version 1 pinned the
	// sha256 of a .wt.toml's bytes; these pin the sha256 of a source's commands.
	// Reading one as the other would compare unrelated hashes, so the only safe
	// reading is "nothing here is approved" — said out loud, because re-approving
	// with no explanation is the failure this file warns about below.
	if store.Version != trustStoreVersion {
		warnStaleTrustStore(store.Version)
		return trustStore{Version: trustStoreVersion}, nil
	}
	return store, nil
}

// staleTrustStoreWarning keeps the format notice to once per process: several
// hook events can fire in one command, and each one reads the store.
var staleTrustStoreWarning sync.Once

func warnStaleTrustStore(found int) {
	staleTrustStoreWarning.Do(func() {
		fmt.Fprintf(os.Stderr,
			"⚠ %s is in an older format (%d, this wt reads %d).\n"+
				"  Those approvals pinned files rather than commands and are not translated.\n"+
				"  wt will ask once more for each set of hooks you use.\n\n",
			trustFilePath(), found, trustStoreVersion)
	})
}

// saveTrustStore writes the trust store, replacing any existing one.
//
// Written via a temp file and rename so an interrupted write cannot leave a
// truncated store behind — which would silently revoke every approval.
func saveTrustStore(store trustStore) error {
	store.Version = trustStoreVersion
	path := trustFilePath()
	if path == "" {
		return errNoTrustStoreDir
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".trust-*.toml")
	if err != nil {
		return fmt.Errorf("failed to write trust store: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.WriteString(tmp, trustStoreHeader); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write trust store: %w", err)
	}
	if err := toml.NewEncoder(tmp).Encode(store); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("failed to write trust store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write trust store: %w", err)
	}
	// 0600: the store is the record of what the user agreed to execute.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("failed to write trust store: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to write trust store: %w", err)
	}
	return nil
}

const trustStoreHeader = `# wt hook trust store — managed by 'wt trust' and 'wt untrust'.
#
# Each entry records a set of hook commands you approved, pinned to the commands
# themselves. Editing them invalidates the entry and wt will ask again. Entries
# scoped to "user config" came from your own config file and apply everywhere;
# the rest are pinned to one repository. Deleting this file revokes everything.

`

// isTrusted reports whether this exact set of commands has been approved for
// this exact scope.
func (s trustStore) isTrusted(scope, sha string) bool {
	if scope == "" || sha == "" {
		return false
	}
	for _, rec := range s.Trusted {
		if rec.Scope == scope && rec.SHA256 == sha {
			return true
		}
	}
	return false
}

// add records an approval, replacing any previous entry with the same scope and
// hash. Entries for the same scope with a *different* hash are kept: worktrees
// of one repo can legitimately sit on branches whose .wt.toml differ, and
// dropping them would make switching between two approved branches re-prompt
// each time.
func (s *trustStore) add(rec trustRecord) {
	for i, existing := range s.Trusted {
		if existing.Scope == rec.Scope && existing.SHA256 == rec.SHA256 {
			s.Trusted[i] = rec
			return
		}
	}
	s.Trusted = append(s.Trusted, rec)
}

// remove drops every approval for a scope and reports how many went.
func (s *trustStore) remove(scope string) int {
	kept := make([]trustRecord, 0, len(s.Trusted))
	for _, rec := range s.Trusted {
		if rec.Scope != scope {
			kept = append(kept, rec)
		}
	}
	removed := len(s.Trusted) - len(kept)
	s.Trusted = kept
	return removed
}

// hashBytes returns the hex sha256 of some contents.
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// hookSetHash is the identity an approval is pinned to: every command a source
// contributes, in event order, folded together with the source's own name.
//
// The whole set rather than the batch about to run, because approving buys
// silence for everything that source will ask for later — a benign post_create
// must not be able to consent on behalf of a pre_remove the user never saw.
//
// Each field is length-prefixed. Commands are supplied by whoever wrote the
// config and may contain newlines; with a plain separator, one command could
// spell out further events and hash as a set nobody approved.
//
// Hashing the commands rather than the file they arrived in means editing
// anything else in that file — a pattern, a [files] entry — does not re-prompt.
// Only the part that is gated is pinned.
func hookSetHash(source string, entries []hookEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	field := func(s string) { fmt.Fprintf(&b, "%d:%s\n", len(s), s) }
	field(source)
	for _, e := range entries {
		field(e.Event)
		field(e.Cmd)
	}
	return hashBytes([]byte(b.String()))
}

// repoTrustKeyFn resolves the identity a trust record is pinned to.
// A variable so tests can inject one without a real repository.
var repoTrustKeyFn = defaultRepoTrustKey

// defaultRepoTrustKey identifies the repository a trust record is pinned to.
//
// Preferred identity is the common git directory: every `wt create` makes a new
// worktree, and trust granted in the main checkout has to survive into the
// worktrees it spawns or the approval prompt becomes noise the user learns to
// click through. --git-common-dir is shared by every worktree of a repo and
// differs between two clones of the same upstream, which is what is wanted.
//
// But a directory can *claim* any common dir: a `.git` file reading
// `gitdir: /path/to/a/repo/you/trust/.git` is enough, and with a byte-identical
// .wt.toml that would inherit the approval and run those commands in the
// claimant's working tree. So the link is only believed when git itself
// registers this worktree against that common dir. When it does not, the key
// falls back to the working tree's own path: trust still works, it just cannot
// be inherited from somewhere else.
func defaultRepoTrustKey() (string, error) {
	top, err := gitToplevel()
	if err != nil {
		return "", err
	}

	commonDir, err := gitCommonDir()
	if err != nil || commonDir == "" {
		return top, nil
	}
	if !worktreeRegistered(commonDir, top) {
		return top, nil
	}
	return commonDir, nil
}

// gitToplevel returns the absolute path of the current working tree's root.
func gitToplevel() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	top := strings.TrimSpace(string(out))
	if top == "" {
		return "", fmt.Errorf("not in a git repository")
	}
	return canonicalPath(top), nil
}

// gitCommonDir returns the absolute path of the repository's common git dir.
func gitCommonDir() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err == nil {
		if dir := strings.TrimSpace(string(out)); dir != "" {
			return canonicalPath(dir), nil
		}
	}

	// --path-format landed in git 2.31; fall back to resolving by hand.
	out, err = exec.Command("git", "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("no common git dir")
	}
	if !filepath.IsAbs(dir) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(cwd, dir)
	}
	return canonicalPath(dir), nil
}

// worktreeRegistered reports whether git lists top as a worktree of the
// repository owning commonDir. A hand-written `.git` file pointing at someone
// else's repository does not appear in that list; a real main or linked worktree
// does.
func worktreeRegistered(commonDir, top string) bool {
	cmd := exec.Command("git", "--git-dir", commonDir, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		path, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !ok {
			continue
		}
		if canonicalPath(path) == top {
			return true
		}
	}
	return false
}

// canonicalPath resolves symlinks so two spellings of the same directory
// compare equal — /tmp and /private/tmp on macOS being the routine case.
// Falls back to a lexical clean when the path cannot be resolved.
func canonicalPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(p)
}

// hookTrust describes the approval state of the hook set one source supplied.
type hookTrust struct {
	scope       string // trustScopeUser, or the repository identity
	source      string // the hookSource* label the commands arrived under
	file        string // the file that supplied them, for display
	sha         string // sha256 of the command set
	trusted     bool
	whitelisted bool // approved by a [trust] rule rather than a stored record
}

// describeHookSource names a hook source for a message.
func describeHookSource(source string) string {
	if source == "" {
		return "an unrecognised source"
	}
	return source
}

// hookSetTrust resolves the approval state of the hook set supplied by source.
func hookSetTrust(source string) (hookTrust, error) {
	t := hookTrust{source: source}

	switch source {
	case hookSourceConfigFile:
		// One scope for the machine: the user's config file is not tied to any
		// repository, so an approval given once should not be asked for again in
		// the next checkout.
		t.scope = trustScopeUser
		t.file = configFilePath
	case hookSourceRepoConfig:
		if configRepoPath == "" || !configRepoFound {
			return hookTrust{}, fmt.Errorf("no repo-level .wt.toml loaded")
		}
		t.file = configRepoPath
		// The identity resolved at config load. Falling back to resolving it now
		// covers callers that set the path globals directly, but the cached value
		// is the one to prefer: see configRepoKey.
		t.scope = configRepoKey
		if t.scope == "" {
			var err error
			if t.scope, err = repoTrustKeyFn(); err != nil {
				return hookTrust{}, err
			}
		}
	default:
		// A source this switch has not been taught about gets no scope, so
		// nothing about it can be remembered and every run asks again. That is
		// the point: adding a source and forgetting to place it here is
		// annoying, never silently permissive.
		return t, nil
	}

	entries := hookSetEntries(source)
	t.sha = hookSetHash(source, entries)
	if t.sha == "" {
		return hookTrust{}, fmt.Errorf("no hook commands recorded for %s", describeHookSource(source))
	}

	// Consulted before the store is opened, so a whitelisted tree keeps working
	// when the store is unreadable: an escape hatch that depends on the
	// machinery it bypasses is not one.
	if t.scope != trustScopeUser && trustWhitelistAllows(t.scope) {
		t.trusted, t.whitelisted = true, true
		return t, nil
	}

	store, err := loadTrustStore()
	if err != nil {
		// Scope, file and hash are still known and still true; only the verdict
		// is missing. Returning them lets a caller that does not need the verdict
		// — prompt-all — still name the file it is asking about.
		return t, err
	}
	t.trusted = store.isTrusted(t.scope, t.sha)
	return t, nil
}

// trustHookSet records an approval for a resolved hook set.
func trustHookSet(t hookTrust) error {
	if t.scope == "" || t.sha == "" {
		return fmt.Errorf("hooks from %s cannot be remembered", describeHookSource(t.source))
	}
	store, err := loadTrustStore()
	if err != nil {
		return err
	}
	store.add(trustRecord{
		Scope:      t.scope,
		Source:     t.source,
		File:       t.file,
		SHA256:     t.sha,
		ApprovedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return saveTrustStore(store)
}

// trustWhitelistAllows reports whether a repository is covered by the [trust]
// table in the user's config file.
//
// The escape hatch exists because strict-by-default without a proportionate
// opt-out does not produce compliance, it produces WT_HOOKS_APPROVE_ALL=1 in a
// shell profile — which disables the gate everywhere, including for the repo
// you cloned this morning. Narrowing that to named trees is strictly better.
//
// Readable from the user's config file only. It is policy that decides whether
// commands run, so a repository being able to add itself would put the lock on
// the inside of the door — the same reason hooks_policy is not read from
// .wt.toml or from git config.
func trustWhitelistAllows(repoKey string) bool {
	if repoKey == "" || repoKey == trustScopeUser {
		return false
	}
	target := trustWhitelistTarget(repoKey)
	for _, entry := range trustExact {
		if p := normaliseTrustPath(entry); p != "" && p == target {
			return true
		}
	}
	for _, entry := range trustPrefixes {
		if p := normaliseTrustPath(entry); p != "" && hasPathPrefix(target, p) {
			return true
		}
	}
	return false
}

// trustWhitelistTarget turns a repository identity into the path a user would
// recognise as "the repo".
//
// The identity is the common git directory for an ordinary clone, so matching
// ~/src/mine against ~/src/mine/repo/.git only works once the .git is stripped.
// Every worktree of a repository shares that identity, which is what makes one
// whitelist entry cover the worktrees wt creates elsewhere on disk.
//
// Canonicalised on the way out, the same as the rules it will be compared with.
// The identity usually arrives canonical already — see gitCommonDir — but a
// whitelist that silently stops matching because one side spelled /tmp and the
// other /private/tmp fails in the direction of running commands the user
// thought they had vetted, so it is not left to the caller.
func trustWhitelistTarget(repoKey string) string {
	if filepath.Base(repoKey) == ".git" {
		repoKey = filepath.Dir(repoKey)
	}
	return canonicalPath(repoKey)
}

// normaliseTrustPath resolves a whitelist entry to something comparable with a
// repository path, or to "" for an entry that names no directory in particular.
//
// Two ways an entry can name no directory in particular, both of which have to
// resolve to "" rather than to something that matches:
//
//   - blank. `prefix = [""]` must not whitelist the filesystem.
//   - collapsing to the filesystem root. Entries go through expandHome, which
//     expands environment variables, so `prefix = ["$SRC/"]` with SRC unset
//     becomes "/" — every repository on the machine, from a line that reads like
//     it names one tree. Nobody writes `prefix = ["/"]` meaning it, and someone
//     who did can say so per-tree instead; disabling the gate machine-wide is
//     not worth being able to express in one character.
func normaliseTrustPath(entry string) string {
	// Whitespace-only entries are the blank line someone left in the list, not a
	// directory. Everything else is used exactly as written: on Unix a trailing
	// space is part of a directory's name, and trimming "/srv/team " to
	// "/srv/team" would hand the rule a wider tree than the one it names.
	//
	// Windows reaches "/srv/team" anyway, because Win32 strips trailing spaces
	// from a path component, so no directory there can be named "team ". That is
	// the same deal as case-insensitivity: the rule still covers exactly the one
	// directory its path names on that machine — wt just does not decide which
	// that is.
	if strings.TrimSpace(entry) == "" {
		return ""
	}
	// A [trust] rule names its directory literally. This is deliberately the one
	// place wt does not expand environment variables.
	//
	// os.ExpandEnv turns anything it cannot resolve into an empty string and the
	// path closes over the gap, which shortens a rule rather than failing it:
	// "$SRC/Users" becomes "/Users", a directory that exists, holds every
	// repository on the machine, and reads as an ordinary rule afterwards. An
	// unset variable is only the obvious way in. "$$" is not an escape — Go
	// passes "$" to the mapper, which is no variable at all; "${}" is malformed
	// and silently eaten; and on Windows %VAR% expansion loops, so a variable
	// whose value names another, unset one collapses on the second pass. Each
	// wants its own special case, and the next syntax would want another.
	// Refusing to expand removes the class: a rule covers exactly what it says.
	if i := strings.IndexAny(entry, "$%"); i >= 0 {
		warnTrustRuleIgnored(entry, fmt.Sprintf(
			"it contains %q, and [trust] rules are literal paths — write the directory out, or start it with ~",
			entry[i]))
		return ""
	}
	expanded := expandTilde(entry)
	if expanded == "" {
		warnTrustRuleIgnored(entry, "it starts with ~ and there is no home directory to resolve that against")
		return ""
	}
	// canonicalPath resolves symlinks; it does not make a path absolute, and
	// nothing downstream does either — a rule is compared against a repository's
	// absolute .git path, so a relative one silently never matches while
	// 'wt trust --list' shows it as resolving fine. Say it names nothing instead.
	// Making it absolute would be worse than useless: it would resolve against
	// whatever directory wt happened to be run from, so the same rule would cover
	// a different tree each time.
	if !filepath.IsAbs(expanded) {
		warnTrustRuleIgnored(entry, "it is not an absolute path, and a rule has to name one directory rather than a different one per working directory")
		return ""
	}
	path := canonicalPath(expanded)
	// filepath.Dir is its own fixed point exactly at a root — "/" on POSIX,
	// `C:\` or a UNC share root on Windows. Unreachable through a variable now,
	// but a rule can still say "/" outright.
	if filepath.Dir(path) == path {
		warnTrustRuleIgnored(entry, fmt.Sprintf("it names %s, the whole filesystem", path))
		return ""
	}
	return path
}

// expandTilde resolves a leading "~" and nothing else. Returns "" when there is
// no home directory to resolve against, so the rule names nothing rather than
// something relative.
func expandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") && !strings.HasPrefix(path, `~\`) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return ""
	}
	return filepath.Join(home, path[1:])
}

// trustRuleWarnings keeps the notice to once per rule per process: several hook
// events can fire in one command, and each one re-reads the whitelist.
var trustRuleWarnings sync.Map

func warnTrustRuleIgnored(entry, reason string) {
	if _, seen := trustRuleWarnings.LoadOrStore(entry, true); seen {
		return
	}
	fmt.Fprintf(os.Stderr,
		"⚠ ignoring [trust] rule %q in %s: %s.\n"+
			"  Until then, repositories it was meant to cover are asked about as usual.\n\n",
		entry, configFilePath, reason)
}

// hasPathPrefix reports whether path is prefix or sits underneath it.
//
// Compared component-wise rather than as a string: a plain strings.HasPrefix
// would read ~/src/mine as covering ~/src/mine-from-the-internet, which is a
// directory the user never named.
func hasPathPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(path, prefix)
}

var (
	trustList     bool
	untrustGlobal bool
)

var trustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Approve the hooks that apply here",
	Long: `Approve the hook commands configured for this directory.

wt runs no hook until you have approved it, whether it came from your own
config file or from a repository's committed .wt.toml. The approval is pinned
to the commands themselves: change them and wt asks again.

Hooks from your config file are approved once for this machine. Hooks from a
repository's .wt.toml are approved for that repository alone, so an identical
file in a repo you cloned this morning does not inherit the answer.

  wt trust             approve the hooks that apply here
  wt trust --list      show every approval on this machine
  wt untrust           revoke this repository's approvals
  wt untrust --global  revoke the approvals for your own config file`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if trustList {
			return runTrustList(cmd)
		}
		return runTrust(cmd)
	},
}

var untrustCmd = &cobra.Command{
	Use:   "untrust",
	Short: "Revoke hook approvals for this repository",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUntrust(cmd)
	},
}

func init() {
	trustCmd.Flags().BoolVar(&trustList, "list", false, "List all approved hooks")
	untrustCmd.Flags().BoolVar(&untrustGlobal, "global", false, "Revoke the approvals for your own config file instead")
}

// runTrust approves every hook set that applies where the user is standing —
// their own config file's and, if there is one, this repository's.
//
// Both, rather than only the repository's, because "wt trust" is what the skip
// message tells the user to run and it should leave nothing still asking. They
// are still recorded separately, so approving here does not widen the
// repository's reach or pin the user's own hooks to this checkout.
func runTrust(cmd *cobra.Command) error {
	sources := loadedHookSources()
	if len(sources) == 0 {
		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{"approved": []any{}})
		}
		fmt.Println("No hooks are configured here, so there is nothing to approve.")
		return nil
	}

	approved := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		t, err := hookSetTrust(source)
		if err != nil {
			return err
		}

		changed := false
		switch {
		case t.whitelisted, t.trusted:
		default:
			// Show what is being approved. Approving without reading is the
			// failure mode this whole mechanism exists to prevent, so put the
			// commands on screen even when the user asked for it explicitly.
			if !isJSONOutput() {
				// No event is firing: wt trust approves the set, not a run.
				printHookApprovalRequest(os.Stdout, hookSetEntries(source), "", t)
			}
			if err := trustHookSet(t); err != nil {
				return err
			}
			changed = true
		}

		approved = append(approved, map[string]any{
			"source":      source,
			"scope":       t.scope,
			"file":        t.file,
			"sha256":      t.sha,
			"trusted":     true,
			"whitelisted": t.whitelisted,
			"changed":     changed,
			// The text path prints the commands before approving them. JSON
			// cannot interleave that, so it carries them instead: a caller that
			// approves without ever being told what it approved is the same
			// failure, whether a person or a script is reading.
			"commands": hookEntriesJSON(hookSetEntries(source)),
		})

		if isJSONOutput() {
			continue
		}
		switch {
		case t.whitelisted:
			fmt.Printf("Already covered by [trust] in %s: %s\n", configFilePath, t.file)
		case !changed:
			fmt.Printf("Already trusted: %s (%s)\n", t.file, source)
		default:
			fmt.Printf("Trusted: %s (%s)\n", t.file, source)
			fmt.Printf("  wt will ask again if these commands change.\n")
		}
	}

	if isJSONOutput() {
		return emitJSONSuccess(cmd, map[string]any{"approved": approved})
	}
	return nil
}

func runUntrust(cmd *cobra.Command) error {
	scope := trustScopeUser
	if !untrustGlobal {
		repo, err := repoTrustKeyFn()
		if err != nil {
			return err
		}
		scope = repo
	}

	store, err := loadTrustStore()
	if err != nil {
		return err
	}
	removed := store.remove(scope)
	if removed > 0 {
		if err := saveTrustStore(store); err != nil {
			return err
		}
	}

	if isJSONOutput() {
		// still_whitelisted, because "removed" alone reads as "gated now" and a
		// whitelisted repository keeps running its hooks regardless.
		return emitJSONSuccess(cmd, map[string]any{
			"scope":             scope,
			"removed":           removed,
			"still_whitelisted": !untrustGlobal && trustWhitelistAllows(scope),
		})
	}
	switch {
	case removed == 0 && untrustGlobal:
		fmt.Println("Nothing to revoke: your config file's hooks are not approved.")
	case removed == 0:
		fmt.Println("Nothing to revoke: this repository has no approved hooks.")
	default:
		fmt.Printf("Revoked %d approval(s) for %s\n", removed, scope)
	}
	// A whitelist rule is not a record, so revoking cannot reach it. Saying so
	// beats letting the user believe the hooks are now gated when they are not —
	// which is just as wrong after "nothing to revoke", where a whitelisted repo
	// never had a record to begin with, so that branch reports it too.
	if !untrustGlobal && trustWhitelistAllows(scope) {
		fmt.Printf("  Note: %s still matches a [trust] rule in %s, so its hooks keep running.\n", trustWhitelistTarget(scope), configFilePath)
	}
	return nil
}

// hookEntriesJSON renders a hook set for JSON callers, in the order it would be
// printed for a human.
func hookEntriesJSON(entries []hookEntry) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		out = append(out, map[string]any{"event": entry.Event, "command": entry.Cmd})
	}
	return out
}

// trustRulesJSON renders [trust] rules with what each one resolves to, mirroring
// the text listing: a rule containing a variable may cover a different tree than
// it appears to, or — when normaliseTrustPath rejects it — none at all, and a
// caller reading only the rule as written could not tell.
func trustRulesJSON(entries []string) []map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		resolved := normaliseTrustPath(entry)
		out = append(out, map[string]any{
			"rule":    entry,
			"path":    resolved,
			"ignored": resolved == "",
		})
	}
	return out
}

func runTrustList(cmd *cobra.Command) error {
	store, err := loadTrustStore()
	if err != nil {
		return err
	}

	if isJSONOutput() {
		records := make([]map[string]any, 0, len(store.Trusted))
		for _, rec := range store.Trusted {
			records = append(records, map[string]any{
				"scope":       rec.Scope,
				"source":      rec.Source,
				"file":        rec.File,
				"sha256":      rec.SHA256,
				"approved_at": rec.ApprovedAt,
			})
		}
		return emitJSONSuccess(cmd, map[string]any{
			"trust_file": trustFilePath(),
			"trusted":    records,
			"whitelist": map[string]any{
				"prefix": trustRulesJSON(trustPrefixes),
				"exact":  trustRulesJSON(trustExact),
			},
		})
	}

	if len(store.Trusted) == 0 {
		fmt.Println("No approved hooks.")
	} else {
		fmt.Printf("Trust store: %s\n\n", trustFilePath())
		for _, rec := range store.Trusted {
			fmt.Printf("  %s\n", rec.File)
			fmt.Printf("    scope:    %s\n", rec.Scope)
			fmt.Printf("    source:   %s\n", rec.Source)
			fmt.Printf("    sha256:   %s\n", rec.SHA256)
			fmt.Printf("    approved: %s\n\n", rec.ApprovedAt)
		}
	}

	// Listed alongside the records because a whitelist rule approves hooks
	// nothing in the store will ever mention. A trust list that only shows what
	// was clicked through would understate what is allowed to run.
	if len(trustPrefixes) > 0 || len(trustExact) > 0 {
		fmt.Printf("Whitelisted by [trust] in %s — hooks under these paths run unasked:\n", configFilePath)
		// Both the rule as written and what it resolves to. A rule containing a
		// variable reads as covering one tree and may cover another, or — when
		// normaliseTrustPath rejects it — none at all, and this listing is where
		// a user goes to find out what is actually allowed to run.
		list := func(label string, entries []string) {
			for _, entry := range entries {
				switch resolved := normaliseTrustPath(entry); resolved {
				case "":
					fmt.Printf("  %s %s  (ignored: names no directory)\n", label, entry)
				case entry:
					fmt.Printf("  %s %s\n", label, entry)
				default:
					fmt.Printf("  %s %s → %s\n", label, entry, resolved)
				}
			}
		}
		list("prefix:", trustPrefixes)
		list("exact: ", trustExact)
		fmt.Println()
	}
	return nil
}

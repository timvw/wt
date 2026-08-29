package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// Config represents the wt configuration file structure.
type Config struct {
	Root        string `toml:"root"`
	RepoRoot    string `toml:"repo_root"`
	Strategy    string `toml:"strategy"`
	Pattern     string `toml:"pattern"`
	Separator   string `toml:"separator"`
	Hooks       Hooks  `toml:"hooks"`
	HooksPolicy string `toml:"hooks_policy"`
	RepoPattern string `toml:"repo_pattern"`
	Files       Files  `toml:"files"`
	Trust       Trust  `toml:"trust"`

	// Context holds path-matched rules supplying environment variables to
	// pattern rendering. Decoded from the user's config file only — see
	// contextRules in context.go.
	Context []ContextRule `toml:"context"`
}

// Files controls materialisation of untracked/ignored files into new worktrees.
//
// Unlike every other setting, the three list keys accumulate across config
// layers instead of replacing: a user who wants .env everywhere plus whatever
// a project adds has no way to express that if the layers overwrite each
// other. See accumulateFilePatterns.
//
// They are also readable from git config, as the multi-valued keys wt.copy,
// wt.link and wt.exclude — see accumulateGitConfigFilePatterns.
type Files struct {
	Copy        []string `toml:"copy"`
	Link        []string `toml:"link"`
	Exclude     []string `toml:"exclude"`
	CopyIgnored bool     `toml:"copy_ignored"`
}

// Trust names trees whose repository hooks may run without being approved
// first — the escape hatch for people who would otherwise reach for
// WT_HOOKS_APPROVE_ALL=1 and disable the gate everywhere, including for the
// repository they cloned this morning.
//
// Readable from the user's config file ONLY. Not from a repo's .wt.toml and not
// from git config: it decides whether commands run, so a repository able to add
// itself to it would put the lock on the inside of the door. Same reasoning as
// hooks_policy — see resolveHooksPolicy.
type Trust struct {
	// Prefix matches a tree and everything under it, component-wise.
	Prefix []string `toml:"prefix"`
	// Exact matches one repository.
	Exact []string `toml:"exact"`
}

// Hooks holds pre/post command hook commands.
type Hooks struct {
	PreCreate    []string `toml:"pre_create"`
	PostCreate   []string `toml:"post_create"`
	PreCheckout  []string `toml:"pre_checkout"`
	PostCheckout []string `toml:"post_checkout"`
	PreRemove    []string `toml:"pre_remove"`
	PostRemove   []string `toml:"post_remove"`
	PrePR        []string `toml:"pre_pr"`
	PostPR       []string `toml:"post_pr"`
	PreMR        []string `toml:"pre_mr"`
	PostMR       []string `toml:"post_mr"`
	PreClone     []string `toml:"pre_clone"`
	PostClone    []string `toml:"post_clone"`
}

// configSource tracks where each config value came from.
type configSource struct {
	Root        string
	RepoRoot    string
	Strategy    string
	Pattern     string
	Separator   string
	RepoPattern string
	HooksPolicy string
	CopyIgnored string
}

// configFilePath is the resolved path to the config file (set during loading).
var configFilePath string

// configFileFound indicates whether the config file was found during loading.
var configFileFound bool

// configRepoPath is the resolved path to the repo-level .wt.toml (set during loading).
var configRepoPath string

// configRepoFound indicates whether a repo-level .wt.toml was found during loading.
var configRepoFound bool

// configRepoKey is the repository identity a trust record is pinned to,
// resolved while the .wt.toml is being read.
//
// Resolved here rather than when a hook is about to run because by then the
// working directory may be gone: `wt remove` fires post_remove hooks after git
// has deleted the worktree the user was standing in, and `git rev-parse` from a
// deleted directory answers nothing. Config load is the last moment the answer
// is reliably available.
var configRepoKey string

// configRepoInstance is the filesystem identity of configRepoKey, captured at
// the same time. A path and command hash are not enough: a repository deleted
// and cloned again at the same path is a different repository and must be
// approved again.
var configRepoInstance string

// configRepoVerified records whether git confirmed that this working tree is a
// worktree of the repository configRepoKey names, resolved alongside it. Only a
// confirmed identity may be matched against a [trust] rule: see repoIdentity.
var configRepoVerified bool

// configSources tracks the origin of each resolved value.
var configSources configSource

// worktreeHooks holds the effective (merged) hook configuration.
var worktreeHooks Hooks

// hookSources records which config layer supplied each hook event's commands.
// The merge below replaces a whole event at a time, so one source per event is
// exact — which is what decides how widely one approval of it reaches.
var hookSources = map[string]string{}

// declaredHooks holds what each source asked for, before merging.
//
// This, rather than the merged result, is what an approval is pinned to. The two
// differ only for the config file, and only where a repo's .wt.toml overrode one
// of its events — but reading the merged result there would make the identity of
// *your* hooks depend on which repository you happened to be standing in, so a
// single .wt.toml overriding post_checkout would re-ask for your whole config
// file and record a second approval under the same scope.
var declaredHooks = map[string]Hooks{}

// trustPrefixes and trustExact are the [trust] whitelist, loaded from the user's
// config file only. See Trust and trustWhitelistAllows.
var (
	trustPrefixes []string
	trustExact    []string
)

// hooksPolicy is the configured hook approval policy (see hookPolicy* in
// hooks.go). Deliberately loaded from the user's config file only.
var hooksPolicy string

// The accumulated [files] pattern lists, in layer order, each pattern tagged
// with the layer that supplied it. See accumulateFilePatterns for why these
// accumulate rather than replace, and resolveFileConfig for the
// .worktreeinclude layer that is added on top of them at use time.
var (
	filesCopy    []layeredPattern
	filesLink    []layeredPattern
	filesExclude []layeredPattern
)

// filesCopyIgnored is the resolved copy_ignored setting. It is a scalar, so
// unlike the lists it follows the ordinary precedence chain.
var filesCopyIgnored bool

// layeredPattern is one [files] pattern together with the config layer it came
// from, so `wt info` can explain why a pattern is in effect.
type layeredPattern struct {
	Pattern string `json:"pattern"`
	Source  string `json:"source"`
}

// Clone placement configuration, loaded by loadWorktreeConfig.
var (
	// reposRoot is the base directory for canonical clones (wt clone).
	reposRoot string
	// repoPattern is the placement layout for canonical clones.
	repoPattern string
)

// configFlag is the --config flag value (set by cobra).
var configFlag string

// gitRepoRootFn returns the git repository root directory.
// It is a variable so tests can inject a fake implementation.
var gitRepoRootFn = defaultGitRepoRoot

// defaultGitRepoRoot uses git rev-parse to find the repo root.
func defaultGitRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// gitConfigScope identifies which git config file a value came from.
type gitConfigScope string

const (
	gitScopeGlobal gitConfigScope = "global"
	gitScopeLocal  gitConfigScope = "local"
)

// gitConfigEntry is one key/value record as git reported it.
//
// A slice rather than a map because two properties are needed that a map cannot
// carry: repeated keys (a context rule sets one `env` key once per variable,
// with `--add`) and the order git listed them in, which is the order rules are
// composed in.
type gitConfigEntry struct {
	// Key is the full dotted name. git lowercases the section and the variable
	// name but preserves the case of any subsection, and that is kept as-is
	// here so a rule's name survives; lowercase for lookups.
	Key   string
	Value string
}

// gitConfigFn reads the wt.* keys from a single git config scope.
// It is a variable so tests can inject a fake implementation.
var gitConfigFn = defaultGitConfig

// gitConfigBoolKeys names the wt.* keys read as git booleans. They are the only
// ones for which a valueless key is meaningful: `[wt]\n\tcopyIgnored` with no
// "=" is how git spells true.
var gitConfigBoolKeys = map[string]bool{
	"wt.copyignored": true,
}

// defaultGitConfig reads wt.* keys from the given git config scope, in the
// order git reports them.
//
// A missing scope is not an error. `git config --get-regexp` exits 1 when no key
// matches, and exits with other non-zero codes when the scope is unavailable
// (for example --local outside a repository). Both mean "nothing configured
// here", so any error yields no entries rather than failing the command. This
// matches how a malformed config file is treated on the TOML path, which is
// also skipped rather than reported.
func defaultGitConfig(scope gitConfigScope) []gitConfigEntry {
	// --null makes the output unambiguous: records are NUL-separated and the
	// key is separated from its value by a newline, so values containing
	// spaces or newlines survive intact. A key set with no value at all has no
	// newline in its record, which is how it is told apart from an empty one.
	cmd := exec.Command("git", "config", "--"+string(scope), "--null", "--get-regexp", `^wt\.`)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var entries []gitConfigEntry
	for _, record := range strings.Split(string(output), "\x00") {
		if record == "" {
			continue
		}
		key, value, hasValue := strings.Cut(record, "\n")
		if !hasValue && !gitConfigBoolKeys[strings.ToLower(key)] {
			// `[wt]\n\tseparator` with no "=": git reports it as valueless.
			// For a string setting that means nothing, so it is treated as
			// unset rather than as an empty string. A valueless boolean is
			// git's spelling of true, and parseGitBool reads the empty value
			// that way, so those are kept.
			continue
		}
		entries = append(entries, gitConfigEntry{Key: key, Value: value})
	}
	return entries
}

// gitConfigValues collapses entries to one value per key, for the scalar
// settings.
//
// The last value wins: git lists multi-valued keys in lowest-to-highest
// precedence order, and for a scalar the final one is the effective value.
// Keys are lowercased so lookups can be written in one spelling.
func gitConfigValues(entries []gitConfigEntry) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		values[strings.ToLower(entry.Key)] = entry.Value
	}
	return values
}

// applyGitConfig applies wt.* scalar settings from one git config scope. The
// [files] list keys arrive separately, via accumulateGitConfigFilePatterns.
//
// The dividing line is not scalar versus list — it is that git config carries
// settings, never commands, nor the policy that gates commands:
//
//   - [hooks] is not read from git config today, at any scope. Not because a
//     multi-valued key could not carry the commands, and not because the merge
//     is undecided (loadWorktreeConfig resolves each event on its own, the
//     highest layer naming an event supplying that event's whole list). What is
//     missing is the other half: a source needs a scope in hookSetTrust before
//     an approval for it can mean anything, and .git/config is not reliably the
//     reader's own file — a repo handed over as a directory rather than a clone
//     brings its .git/config along (see the trust store rationale in trust.go),
//     so --local and --global would not deserve the same scope. Adding the
//     source without that is safe but useless: approveHooks reaches its default
//     branch, and every run asks again with nothing able to remember the answer.
//   - hooks_policy and [trust] are never read from git config, for the reason in
//     resolveHooksPolicy: they are the gate on that execution, and a repository
//     choosing how closely wt scrutinises its own hooks defeats the mechanism.
//   - [files] copy/link/exclude may come in, because they are declarative data
//     bounded by invariants F1-F7 rather than commands. The copy universe is
//     the ignored set of the main worktree, a strict subset of what
//     wt.copyIgnored already selects from this same scope with no gate.
//
// Key spelling: a git config key is the TOML key with any section flattened and
// multi-word names camelCased — wt.repoRoot, wt.repoPattern, wt.copyIgnored.
// git config variable names allow only alphanumerics and "-", and git rejects an
// underscore outright ("error: invalid key"), a whole config file holding one
// included: a single `repo_root =` line makes every wt.* key in that scope
// unreadable. git lowercases the name it reports, hence the lookup keys below.
// TestAdvertisedGitConfigKeysAreSettable pins this against real git.
func applyGitConfig(entries []gitConfigEntry, sourceLabel string) {
	values := gitConfigValues(entries)
	if len(values) == 0 {
		return
	}

	if v := strings.TrimSpace(values["wt.root"]); v != "" {
		worktreeRoot = expandHome(v)
		configSources.Root = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.reporoot"]); v != "" {
		reposRoot = expandHome(v)
		configSources.RepoRoot = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.strategy"]); v != "" {
		worktreeStrategy = strings.ToLower(v)
		configSources.Strategy = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.pattern"]); v != "" {
		worktreePattern = v
		configSources.Pattern = sourceLabel
	}
	// separator is applied even when empty: an empty separator is a meaningful
	// value, matching how WORKTREE_SEPARATOR is handled.
	if v, ok := values["wt.separator"]; ok {
		worktreeSeparator = v
		configSources.Separator = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.repopattern"]); v != "" {
		repoPattern = v
		configSources.RepoPattern = sourceLabel
	}
	// The [files] scalar. Its list siblings are not read here because they do
	// not collapse to one value per key — see accumulateGitConfigFilePatterns.
	if v, ok := values["wt.copyignored"]; ok {
		if b, ok := parseGitBool(v); ok {
			filesCopyIgnored = b
			configSources.CopyIgnored = sourceLabel
		}
	}
}

// parseGitBool interprets a git-config boolean. git accepts several spellings
// for each value, and a key present with no value means true.
func parseGitBool(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1", "":
		return true, true
	case "false", "no", "off", "0":
		return false, true
	default:
		return false, false
	}
}

// accumulateFilePatterns appends patterns to an accumulated list, tagging each
// with the layer that supplied it and dropping duplicates.
//
// Accumulating rather than replacing is deliberate and is the only sane
// behaviour here: a user whose own config says "always copy .env" and who then
// works in a repo whose .wt.toml adds "copy config/local.yml" wants both, and
// with replace semantics the repo would silently drop the .env. Excludes
// accumulate for the mirror-image reason — a global "never copy *.pem" has to
// hold against any repository's config.
//
// First-seen order is preserved, so the effective list reads in layer order:
// git config (global), config file, repo config, git config (local), then
// .worktreeinclude. A pattern contributed by more than one layer is listed once
// and credited to the first — the lowest — that supplied it.
//
// That order is for display and attribution only. It cannot decide which files
// are materialised, because every matcher built from these lists is pure
// any-match: copy negations are hoisted into a deny matcher applied
// unconditionally, and exclude and link reject negation outright (see
// splitCopyNegations and validateFilePatterns). A deny therefore beats a
// positive pattern from any layer, including a higher one.
func accumulateFilePatterns(dst []layeredPattern, patterns []string, source string) []layeredPattern {
	for _, p := range patterns {
		// Patterns are kept verbatim, including blank ones: gitignore gives a
		// trailing space meaning (it is stripped unless escaped, as in
		// "file\ "), so trimming here would turn "file\ " into "file\" before
		// the ignore parser ever saw it. A blank entry is not dropped here
		// either — a blank *line* in .worktreeinclude is nothing at all and
		// ignore.ParseFile has already discarded it, so anything blank that
		// reaches this point was written out as an entry, which is a mistake
		// worth naming. validateFilePatterns reports it with its source.
		duplicate := false
		for _, existing := range dst {
			if existing.Pattern == p {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		dst = append(dst, layeredPattern{Pattern: p, Source: source})
	}
	return dst
}

// fileListForGitConfigKey returns the accumulated list a git config key feeds,
// or nil for any key that is not one of them.
//
// The comparison is against lowercase spellings because that is how git reports
// a variable name. The spellings drop the [files] section rather than prefixing
// it, following wt.copyIgnored, which is [files] copy_ignored.
func fileListForGitConfigKey(key string) *[]layeredPattern {
	switch strings.ToLower(key) {
	case "wt.copy":
		return &filesCopy
	case "wt.link":
		return &filesLink
	case "wt.exclude":
		return &filesExclude
	}
	return nil
}

// accumulateGitConfigFilePatterns folds the [files] list keys from one git
// config scope into the accumulated lists.
//
// It reads the raw entries rather than the gitConfigValues map, because these
// keys are multi-valued: `git config --add wt.copy` twice is two patterns, and
// collapsing to one value per key would silently keep only the last. git
// reports repeats in file order, and defaultGitConfig preserves them.
//
// Values are taken verbatim: a pattern's leading "!" and trailing whitespace
// both carry meaning to the ignore parser. An empty value is kept too, and
// validateFilePatterns rejects it naming this scope — `git config --add wt.copy
// ""` is a mistake, and silently doing nothing about it is how you spend an
// afternoon wondering why the copy list is not growing. A *valueless* key is a
// different thing and defaultGitConfig has already dropped it.
func accumulateGitConfigFilePatterns(entries []gitConfigEntry, sourceLabel string) {
	for _, entry := range entries {
		list := fileListForGitConfigKey(entry.Key)
		if list == nil {
			continue
		}
		*list = accumulateFilePatterns(*list, []string{entry.Value}, sourceLabel)
	}
}

// defaultConfigTemplate is the content written by `wt config init`.
const defaultConfigTemplate = `# wt configuration file
# See: https://github.com/timvw/wt#configuration

# Root directory for worktrees (default: ~/dev/worktrees)
# root = "~/dev/worktrees"

# Base directory for canonical clones created by 'wt clone' (default: ~/dev/repos).
# repo_root = "~/dev/repos"

# Worktree placement strategy
# Options: global, sibling-repo, parent-branches, parent-worktrees,
#          parent-dotdir, inside-dotdir, custom
# strategy = "global"

# Custom pattern (used when strategy = "custom", or to override any strategy's default)
# Available variables: {.worktreeRoot}, {.repo.Name}, {.repo.Main},
#                      {.repo.Owner}, {.repo.Host}, {.branch},
#                      {.env.VARNAME} (access environment variables, e.g. {.env.USER})
# pattern = "{.worktreeRoot}/{.repo.Name}/{.branch}"

# Separator replaces "/" and "\" in template value variables ({.branch}, {.repo.Owner}, {.env.*})
# Default is "/" (no transformation — slashes create subdirectories).
# Set to "-" or "_" for flat paths (e.g. feat/foo -> feat-foo).
# Does NOT affect path variables ({.repo.Main}, {.worktreeRoot}).
# separator = "/"

# Example: group worktrees by a FEATURE environment variable
# strategy = "custom"
# pattern = "{.worktreeRoot}/{.env.FEATURE}/{.repo.Name}"

# Placement layout for repos cloned by 'wt clone'. Variables: {.repoRoot},
# {.repo.Host}, {.repo.Owner}, {.repo.Name}, {.branch}, {.env.VARNAME}.
# {.branch} is the remote's default branch (via git ls-remote, fallback "main"),
# which makes the clone a valid main-worktree slot for sibling strategies.
# repo_pattern = "{.repoRoot}/{.repo.Host}/{.repo.Owner}/{.repo.Name}/{.branch}"

# Example: add your own grouping level (a "category", a client, a year) with an
# environment variable, then override it per invocation:
#   WT_CATEGORY=work wt clone o/r
# Give it a default in your shell rc: referencing an unset variable is an error,
# while an empty value just collapses the segment away.
# repo_pattern = "{.repoRoot}/{.env.WT_CATEGORY}/{.repo.Owner}/{.repo.Name}/{.branch}"

# Set variables for a whole tree of repositories, instead of exporting them per
# command. Each rule matches a path prefix; every matching rule applies, and
# later rules override earlier ones per variable. An exported variable always
# wins over a rule.
#
# Matched against the repository's main checkout for worktree commands, and
# against the current directory for 'wt clone' (no repo exists yet).
#
# The same rules can live in ~/.gitconfig instead (this file wins on conflict):
#   git config --global wt.context.work.whenpath "~/dev/repos/work"
#   git config --global --add wt.context.work.env "WT_CATEGORY=work"
#
# [[context]]
# when_path = "~/dev/repos/work"
# env = { WT_CATEGORY = "work" }
#
# [[context]]
# when_path = "~/dev/repos/personal"
# env = { WT_CATEGORY = "personal" }

# Files — materialise untracked files into every new worktree
# Runs on create/checkout/pr/mr, after 'git worktree add' and before post_* hooks.
# Patterns use gitignore syntax and are relative to the main worktree root.
# Only untracked, git-ignored files are candidates: tracked files are already in
# the new worktree via the checkout and are never touched.
# Copies use a reflink on APFS/Btrfs/XFS, so size is not a reason to hesitate.
# An existing destination file is skipped, never overwritten (use 'wt copy --force').
# Skip once with --no-copy; switch the feature off with WT_FILES_DISABLED=1.
#
# The three list keys are a union across every layer — this file, git config
# (wt.copy / wt.link / wt.exclude, multi-valued, 'git config --add' per pattern),
# a repo's .wt.toml and its .worktreeinclude — rather than one replacing another.
# 'exclude' is applied last and always wins. To drop a pattern a lower layer
# contributed, deny it: a "!" entry in 'copy' beats every layer's copy patterns.
#
# [files]
# copy = [".env", ".envrc", ".claude/settings.local.json"]
# link = ["node_modules", ".venv"]
# exclude = ["*.pem", "*.key"]
# copy_ignored = false

# Hooks — run commands before/after wt operations
# Available env vars in hooks: $WT_PATH, $WT_BRANCH, $WT_MAIN,
#                              $WT_REPO_NAME, $WT_REPO_HOST, $WT_REPO_OWNER
# Pre-hooks abort on failure; post-hooks warn only.
# Set WT_HOOKS_DISABLED=1 to skip all hooks.
#
# No hook runs until you approve it ('wt trust'), including the ones you write
# in this file — an approval is what says a command is yours, and a file is not
# evidence of that on its own. Approving is once per set of commands; editing a
# command asks again.
#   prompt-untrusted  (default) show anything not yet approved, and ask
#   prompt-all        ask every time, even for hooks already approved
#   trusted-only      never ask; skip anything not already approved (CI)
#   off               run no hooks at all
# To approve a whole tree ahead of time instead, see [trust] in
# docs/configuration.md.
# hooks_policy = "prompt-untrusted"
# NOTE: Always quote path variables ("$WT_PATH") to handle spaces in paths.
#
# [hooks]
# post_create = ["cd \"$WT_PATH\" && direnv allow"]
# post_checkout = ["cd \"$WT_PATH\" && npm install"]
# pre_remove = ["echo \"Removing $WT_PATH\""]
# post_clone = ["cd \"$WT_PATH\" && git status"]
`

// configDir returns the directory where wt config files are stored, or "" when
// no absolute location can be determined.
//
// The result is always absolute or empty. Per the XDG Base Directory spec a
// relative XDG_CONFIG_HOME is invalid and must be ignored, and an unset HOME
// leaves nothing to fall back to. Resolving either against the working directory
// would place wt's config — and, worse, its trust store — inside whatever
// repository the user happens to be standing in, which would let a cloned repo
// ship approvals for its own hooks. Returning "" instead fails closed: no config
// file is found and nothing is trusted.
//
// filepath.IsAbs is the test rather than "starts with a separator", because on
// Windows a rooted path with no volume ("\config") is resolved against the
// current drive and is not a location wt can be sure of either.
func configDir() string {
	// namesOneDirectory, not IsAbs: this is where the trust store comes from, so
	// XDG_CONFIG_HOME=/proc/self/cwd/.config would have wt read its record of
	// what you have approved out of whatever repository you are standing in. A
	// repository committing .config/wt/trust.toml with its own scope and hash
	// then arrives pre-approved. Refusing the config FILE and not the directory
	// beneath it would have closed the smaller half of that.
	if d := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); d != "" {
		if namesOneDirectory(d) {
			return filepath.Join(filepath.Clean(d), "wt")
		}
		warnConfigHomeNotAbsolute("XDG_CONFIG_HOME", d)
	}
	if runtime.GOOS == "windows" {
		if d := strings.TrimSpace(os.Getenv("APPDATA")); d != "" {
			if namesOneDirectory(d) {
				return filepath.Join(filepath.Clean(d), "wt")
			}
			warnConfigHomeNotAbsolute("APPDATA", d)
		}
	}
	// And the same test on the fallback, which is the half of this that the two
	// checks above would otherwise leave open: HOME=/proc/self/cwd is absolute,
	// so IsAbs said yes and the trust store came out of the repository — the
	// override refused, the default walked in behind it. A guard that asks the
	// question of the overrides and not of the answer they fall back to is not
	// asking the question.
	home, err := os.UserHomeDir()
	if err != nil || !namesOneDirectory(home) {
		return ""
	}
	return filepath.Join(home, ".config", "wt")
}

// configHomeWarnings keeps the notice to once per variable per process:
// configDir is consulted by the config loader and again by every hook event.
var configHomeWarnings sync.Map

// warnConfigHomeNotAbsolute reports an ignored override. Saying nothing would
// leave the user reading a config file they did not write, or re-approving hooks
// they already approved, with no way to tell why.
func warnConfigHomeNotAbsolute(name, value string) {
	if _, seen := configHomeWarnings.LoadOrStore(name, true); seen {
		return
	}
	// Two reasons, because "not absolute" would be a lie about /proc/self/cwd,
	// and a warning the user can see is wrong is one they learn to skip.
	why := "is not an absolute path"
	if filepath.IsAbs(value) {
		why = "names a different directory depending on which process asks"
	}
	fmt.Fprintf(os.Stderr,
		"⚠ %s is set to %q, which %s, so it is being ignored.\n"+
			"  wt is falling back to your home directory.\n\n",
		name, value, why)
}

// configFileInRepo reports whether the config file wt is about to read lives
// inside the repository it is about to gate.
//
// Compared lexically, on the path as written rather than the path resolved: a
// config file kept in a dotfiles repository and symlinked to ~/.config/wt is the
// ordinary setup, and it must keep working while you are standing in that
// repository. What must not work is a path that names a file inside this
// repository, because the repository chose its contents — and the file reached
// via a relative WT_CONFIG always does.
//
// Case-folded, because os.Open is: on macOS and Windows an absolute path that
// spells the repository's own directory in a different case opens the very same
// committed file, and a byte comparison would call it yours. Nor is sameFile a
// backstop here — it only knows about .wt.toml, and this is about the file a
// repository named something else.
func configFileInRepo(configPath, repoRoot string) bool {
	if configPath == "" || repoRoot == "" {
		return false
	}
	abs, err := filepath.Abs(configPath)
	if err != nil {
		// Unresolvable means unknowable, and this decides whether a repository
		// gets to supply the gate's own settings.
		return true
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return true
	}
	if hasPathPrefixFold(filepath.Clean(abs), filepath.Clean(root)) {
		return true
	}
	// A name is not the only way to spell a directory. macOS firmlinks give the
	// repository a second absolute path (/System/Volumes/Data/Users/alice/src/x
	// is /Users/alice/src/x), a Linux bind mount does the same, and os.Open
	// honours either — so the containment has to be settled by identity too.
	//
	// Deliberately the directories the file sits in, not the file: a config
	// symlinked out to a dotfiles repository is the ordinary setup this stays
	// lexical for, and following the link would be what refuses it.
	return dirWithin(filepath.Dir(abs), root)
}

// configFileSuppliedByRepo reports whether the config file wt is about to read
// is one the repository it is about to gate gets to write.
//
// repoRoot is the working tree wt is standing in, or "" when it is not in one.
// Every other working tree of the same repository counts too — each holds the
// repository's files at its own path, and a config file is refused for what
// supplies it, not for which directory wt happens to have been run from.
// pathIsProcessRelative reports whether an absolute path names a different file
// depending on the process reading it.
//
// /proc/self and /proc/thread-self are the kernel's names for "whoever is
// asking", and cwd and root beneath them are that process's working directory
// and filesystem root. /proc/self/cwd/config.toml is therefore ./config.toml
// wearing an absolute spelling — it passes IsAbs, and it walks past a
// containment test looking for a repository in its parents, because its parents
// are /proc/self and /proc.
//
// Linux only in effect, and checked everywhere: /proc/self is not a path anyone
// writes by accident on a machine where it means nothing.
// namesOneDirectory reports whether a path means the same place wherever wt is
// run from — which is what every "is it absolute?" test in wt was really asking.
//
// filepath.IsAbs answers a question about spelling. /proc/self/cwd is spelled
// absolutely and means the working directory, so it passes that test and fails
// the one that matters. Anywhere wt accepts a path BECAUSE it is absolute — the
// config file, the config directory that holds the trust store, git's global
// config — it wants this instead.
func namesOneDirectory(path string) bool {
	return filepath.IsAbs(path) && !pathIsProcessRelative(path)
}

func pathIsProcessRelative(path string) bool {
	rest, ok := strings.CutPrefix(filepath.ToSlash(filepath.Clean(path)), "/proc/")
	if !ok {
		return false
	}
	who, _, _ := strings.Cut(rest, "/")
	if who == "self" || who == "thread-self" {
		return true
	}
	// A numeric pid is the same trick aimed at another process. /proc/<shell
	// pid>/cwd follows the shell around, which is inside the repository just as
	// surely as wt's own working directory is, and nothing about "self" was what
	// made the first spelling wrong.
	return who != "" && strings.IndexFunc(who, func(r rune) bool { return r < '0' || r > '9' }) < 0
}

func configFileSuppliedByRepo(configPath, repoRoot string) bool {
	if configPath == "" {
		return false
	}
	for _, root := range repoWorkingTrees(repoRoot) {
		if configFileInRepo(configPath, root) {
			return true
		}
		if sameFile(configPath, filepath.Join(root, ".wt.toml")) {
			return true
		}
	}
	return false
}

// repoWorkingTrees returns the directories whose contents the repository wt is
// standing in gets to choose.
//
// The working tree wt is in comes first and is always included, so a git that
// cannot list anything leaves the guard no weaker than it was. The main
// checkout is derived from the common git directory rather than listed, so it
// is covered even then — it is the one a WT_CONFIG typed once and left in a
// shell profile is most likely to name.
func repoWorkingTrees(repoRoot string) []string {
	var roots []string
	if repoRoot != "" {
		roots = append(roots, repoRoot)
	}
	commonDir, err := gitCommonDir()
	if err != nil || commonDir == "" {
		return roots
	}
	roots = append(roots, commonDir, filepath.Dir(commonDir))
	roots = append(roots, gitWorktreePaths(commonDir)...)
	return append(roots, superprojectWorkingTrees()...)
}

// superprojectWorkingTrees names the working trees of the repositories this one
// is a submodule of, outermost last.
//
// A submodule is a repository, and the superproject holding it is not the user —
// it is more committed content, chosen by whoever chose the submodule. So
// `WT_CONFIG=../../wt-user.toml` reaching a file the superproject committed is
// the repo-config layer read as the user config file, the same finding as the
// linked-worktree one, one level out. Such a file could whitelist the tree it
// sits in, and the submodule's own verified scope beneath <super>/.git/modules
// would then match the rule.
//
// Walked rather than asked once, because submodules nest.
func superprojectWorkingTrees() []string {
	var roots []string
	dir := ""
	for range 32 {
		cmd := exec.Command("git", "rev-parse", "--show-superproject-working-tree")
		if dir != "" {
			cmd.Dir = dir
		}
		out, err := cmd.Output()
		if err != nil {
			return roots
		}
		super := gitOutputPath(out)
		if super == "" || !filepath.IsAbs(super) {
			return roots
		}
		roots = append(roots, super)
		dir = super
	}
	return roots
}

// sameFile reports whether two paths name the same file on disk.
//
// Compared by identity rather than by name: a config path can be given relative
// to the working directory, reached through a symlink, or spelled in a different
// case on a case-insensitive filesystem, and each is the same file under a name
// that does not compare equal.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}

// warnRepoConfigIsNotYourConfig explains why a --config or WT_CONFIG pointing at
// the repository's own .wt.toml was not read as the config file. Said out loud
// because the alternative is settings that appear to be ignored at random: the
// same WT_CONFIG works in a directory that is not a repository.
func warnRepoConfigIsNotYourConfig(path string) {
	fmt.Fprintf(os.Stderr,
		"⚠ %s is a file in this repository, so it is not being read as your config file.\n"+
			"  A repository read as your config file could exempt itself from hook approval.\n"+
			"  Its settings still apply as a repository's, and its hooks still need 'wt trust'.\n\n",
		path)
}

// warnConfigPathNotAbsolute reports a --config or WT_CONFIG that names a
// different file depending on where wt was run.
func warnConfigPathNotAbsolute(path string) {
	fmt.Fprintf(os.Stderr,
		"⚠ %s is not an absolute path, so it is not being read as your config file.\n"+
			"  A relative config path names a different file in every directory wt runs in,\n"+
			"  which lets whatever is checked out there supply one — and a config file can\n"+
			"  exempt hooks from approval. Give the full path instead.\n\n",
		path)
}

// resolveConfigPath determines which config file to use.
// Priority: --config flag > WT_CONFIG env var > default location.
func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envPath := os.Getenv("WT_CONFIG"); envPath != "" {
		return envPath
	}
	// "" rather than a bare "config.toml": with no absolute directory to join
	// onto, a relative name would be read from the current working directory —
	// that is, from inside whatever repository wt was run in.
	dir := configDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "config.toml")
}

// loadWorktreeConfig loads configuration from git config, files and environment
// variables.
//
// Precedence, highest first:
//
//	env vars > local git config > repo .wt.toml > config file > global git
//	config > defaults
//
// --config and WT_CONFIG are not a precedence layer of their own: they select
// which TOML file is loaded, and its values still sit below .wt.toml and env.
func loadWorktreeConfig() {
	// 1. Start with defaults
	home, _ := os.UserHomeDir()
	defaultRoot := filepath.Join(home, "dev", "worktrees")

	worktreeRoot = defaultRoot
	reposRoot = filepath.Join(home, "dev", "repos")
	worktreeStrategy = "global"
	worktreePattern = ""
	worktreeSeparator = "/"

	configSources = configSource{
		Root:        "default",
		RepoRoot:    "default",
		Strategy:    "default",
		Pattern:     "default",
		Separator:   "default",
		RepoPattern: "default",
		HooksPolicy: "default",
		CopyIgnored: "default",
	}

	// Reset hooks
	worktreeHooks = Hooks{}
	hookSources = map[string]string{}
	declaredHooks = map[string]Hooks{}
	hooksPolicy = ""
	trustPrefixes = nil
	trustExact = nil
	contextRules = nil

	// Reset [files]
	filesCopy = nil
	filesLink = nil
	filesExclude = nil
	filesCopyIgnored = false

	repoPattern = defaultRepoPattern

	// 2. Global git config (~/.gitconfig) — the broadest fallback, below wt's
	// own config file. Read once and used three times: the scalar settings, the
	// [files] list keys, and the wt.context.* rules, which no other git scope
	// may supply.
	globalGit := gitConfigFn(gitScopeGlobal)
	applyGitConfig(globalGit, "git config (global)")
	accumulateGitConfigFilePatterns(globalGit, "git config (global)")
	contextRules = contextRulesFromGitConfig(globalGit)

	// 3. Load config file
	configFilePath = resolveConfigPath(configFlag)
	configFileFound = false

	// The repository, located once and used twice: to load its .wt.toml as the
	// repo layer in step 4, and — first — to be sure the repository is not also
	// about to be read as the user's config file.
	repoRoot, repoRootErr := gitRepoRootFn()
	repoConfigPath := ""
	if repoRootErr == nil {
		repoConfigPath = filepath.Join(repoRoot, ".wt.toml")
	}

	switch {
	// Two layers, one file, and the outer one is not gated: [trust] and
	// hooks_policy are honoured from the config file precisely because it is
	// yours, so a repository read as your config file can whitelist its own path
	// and run its own hooks unasked.
	//
	// A relative path is the way in, and it does not have to name a file in the
	// repository to get there. "../wt-user.toml" is outside the current
	// repository by every containment test — and inside the superproject that
	// vendored it, which is where a submodule's hooks would be exempted from.
	// One setting, a different file in every directory wt is run from, chosen by
	// whatever is checked out there. There is no legitimate version of this: a
	// config file lives at one path. Say so before asking where that path is.
	case configFilePath != "" && !filepath.IsAbs(configFilePath):
		warnConfigPathNotAbsolute(configFilePath)

	// Absolute and relative anyway. /proc/self/cwd is the working directory
	// under a name that survives every containment test, because those walk the
	// path's lexical parents and /proc has none of the repository in it: enter a
	// subdirectory of a repository and a committed config.toml is read as yours,
	// [trust] rules and all. The same goes for /proc/self/root.
	//
	// Named rather than resolved, deliberately. Resolving would also catch the
	// config file people legitimately symlink out of a dotfiles repo they are
	// standing in, which is the one symlink wt allows — see the case below.
	// What is wrong here is not where the path leads, it is that where it leads
	// depends on when you ask.
	case configFilePath != "" && filepath.IsAbs(configFilePath) && !namesOneDirectory(configFilePath):
		warnConfigPathNotAbsolute(configFilePath)

	// The same thing spelled absolutely, which is usually a mistake rather than
	// an attack — WT_CONFIG=$PWD/.wt.toml — but reads the repository's file as
	// yours just the same. The name is not the point: WT_CONFIG=wt-user.toml is
	// a file a repository can commit as easily as .wt.toml.
	//
	// Asked of every working tree of the repository rather than only the one wt
	// is standing in. A repository supplies the file in all of them, and wt's
	// whole job is moving you between them: a WT_CONFIG naming the main
	// checkout's .wt.toml would be refused there and then quietly honoured after
	// 'wt checkout', which is the direction that runs the commands.
	//
	// The second test is the same file reached from outside the repository — a
	// config symlinked to a checked-in .wt.toml — which no comparison of names
	// would catch. That one is about scope rather than about an attacker:
	// .wt.toml is the file a repository configures wt with by convention, so
	// reading it as your config too would hand a repo-owned file the user-config
	// scope, where an approval covers every repository at once and hooks_policy
	// is honoured.
	//
	// Deliberately only .wt.toml, and deliberately not every file a symlink might
	// resolve into. A config kept in a dotfiles repository and symlinked into
	// ~/.config/wt is the ordinary setup: refusing it while you stand in that
	// repository would take your root and pattern with it and put the worktree
	// somewhere else. Nothing is given away by allowing it, either — that file is
	// your config in every other repository already, so a commit that turns it
	// hostile has you regardless of what wt does inside the one it lives in.
	case configFileSuppliedByRepo(configFilePath, repoRoot):
		warnRepoConfigIsNotYourConfig(configFilePath)

	default:
		if _, err := os.Stat(configFilePath); err == nil {
			configFileFound = true
			var cfg Config
			if md, err := toml.DecodeFile(configFilePath, &cfg); err == nil {
				if cfg.Root != "" {
					worktreeRoot = expandHome(cfg.Root)
					configSources.Root = "config file"
				}
				if cfg.RepoRoot != "" {
					reposRoot = expandHome(cfg.RepoRoot)
					configSources.RepoRoot = "config file"
				}
				if cfg.Strategy != "" {
					worktreeStrategy = strings.ToLower(strings.TrimSpace(cfg.Strategy))
					configSources.Strategy = "config file"
				}
				if cfg.Pattern != "" {
					worktreePattern = strings.TrimSpace(cfg.Pattern)
					configSources.Pattern = "config file"
				}
				if cfg.Separator != "" {
					worktreeSeparator = cfg.Separator
					configSources.Separator = "config file"
				}
				if cfg.RepoPattern != "" {
					repoPattern = strings.TrimSpace(cfg.RepoPattern)
					configSources.RepoPattern = "config file"
				}
				worktreeHooks = cfg.Hooks
				declaredHooks[hookSourceConfigFile] = cfg.Hooks
				for _, event := range hookEvents {
					if len(hooksOf(cfg.Hooks, event)) > 0 {
						hookSources[event] = hookSourceConfigFile
					}
				}
				if cfg.HooksPolicy != "" {
					hooksPolicy = strings.ToLower(strings.TrimSpace(cfg.HooksPolicy))
					configSources.HooksPolicy = "config file"
				}
				// Only from here. Loading [trust] anywhere else would let the thing
				// being gated name itself as exempt.
				trustPrefixes = cfg.Trust.Prefix
				trustExact = cfg.Trust.Exact
				// Appended after the git config rules rather than replacing them,
				// so the two sources compose the way rules within one source
				// already do: every matching rule applies, later definitions win
				// per variable. Because these come last, the config file wins
				// wherever both sources set the same variable for the same path —
				// which is the documented precedence — while a git config rule for
				// some unrelated tree keeps working instead of silently vanishing
				// the moment a [[context]] block is added to this file.
				contextRules = append(contextRules, cfg.Context...)

				filesCopy = accumulateFilePatterns(filesCopy, cfg.Files.Copy, "config file")
				filesLink = accumulateFilePatterns(filesLink, cfg.Files.Link, "config file")
				filesExclude = accumulateFilePatterns(filesExclude, cfg.Files.Exclude, "config file")
				// IsDefined rather than a non-zero check: copy_ignored is a bool,
				// so "written as false" and "not written" are the same value and
				// only the metadata can tell them apart.
				if md.IsDefined("files", "copy_ignored") {
					filesCopyIgnored = cfg.Files.CopyIgnored
					configSources.CopyIgnored = "config file"
				}
			}
		}
	}

	// 4. Load repo-level .wt.toml (overrides global config, but NOT root and
	//    NOT the clone settings). `wt clone` acquires a repository unrelated to
	//    whichever one you happen to be standing in, so letting that repo's
	//    .wt.toml redirect the destination or run clone hooks would be wrong.
	configRepoPath = ""
	configRepoFound = false
	configRepoKey = ""
	configRepoInstance = ""
	configRepoVerified = false

	if repoRootErr == nil {
		configRepoPath = repoConfigPath
		// Read once, and let the approval be pinned to what this read decoded
		// rather than to a fresh read when the hooks are about to run: re-reading
		// later would leave a window in which the file is swapped, and wt would
		// check one file's contents while running another file's commands.
		if data, err := os.ReadFile(repoConfigPath); err == nil {
			configRepoFound = true
			if id, err := repoTrustKeyFn(); err == nil {
				configRepoKey, configRepoInstance, configRepoVerified = id.key, id.instance, id.verified
			}
			var repoCfg Config
			if md, err := toml.Decode(string(data), &repoCfg); err == nil {
				// root, repo_root and repo_pattern are intentionally NOT loaded
				// from repo config
				if repoCfg.Strategy != "" {
					worktreeStrategy = strings.ToLower(strings.TrimSpace(repoCfg.Strategy))
					configSources.Strategy = "repo config"
				}
				if repoCfg.Pattern != "" {
					worktreePattern = strings.TrimSpace(repoCfg.Pattern)
					configSources.Pattern = "repo config"
				}
				if repoCfg.Separator != "" {
					worktreeSeparator = repoCfg.Separator
					configSources.Separator = "repo config"
				}
				// hooks_policy is deliberately NOT read from repo config: a
				// repository choosing how closely wt scrutinises that same
				// repository's hooks would defeat the point.

				// Merge hooks: repo hooks override per-hook type, unset hooks keep
				// global values. Which layer won is recorded in hookSources, which
				// is how an approval for these commands stays pinned to this
				// repository rather than following the user everywhere (see
				// hookSetTrust). [trust] is deliberately not read here for the
				// same reason.
				repoHooks := repoCfg.Hooks
				// pre_clone/post_clone are deliberately not merged from repo
				// config: clone targets a different repository than this one.
				repoHooks.PreClone = nil
				repoHooks.PostClone = nil
				declaredHooks[hookSourceRepoConfig] = repoHooks
				for _, event := range hookEvents {
					cmds := hooksOf(repoHooks, event)
					if len(cmds) == 0 {
						continue
					}
					setHooks(&worktreeHooks, event, cmds)
					hookSources[event] = hookSourceRepoConfig
				}

				// [files] needs no trust gate. Hooks are arbitrary command
				// execution, which is why they have one; [files] is
				// declarative data whose only power is to move bytes from the
				// main worktree into the new worktree — see the F1-F7
				// invariants in files.go. Gating it would also defeat its main
				// purpose, which is exactly the team-config case: a repo
				// declaring "everyone needs .env here".
				filesCopy = accumulateFilePatterns(filesCopy, repoCfg.Files.Copy, "repo config")
				filesLink = accumulateFilePatterns(filesLink, repoCfg.Files.Link, "repo config")
				filesExclude = accumulateFilePatterns(filesExclude, repoCfg.Files.Exclude, "repo config")
				if md.IsDefined("files", "copy_ignored") {
					filesCopyIgnored = repoCfg.Files.CopyIgnored
					configSources.CopyIgnored = "repo config"
				}
			}
		}
	}

	// 5. Local git config (.git/config) — personal, machine-local settings that
	// are not committed, so they outrank the committed .wt.toml. Unlike
	// .wt.toml, this may set root: it is user-controlled local state rather
	// than project policy arriving via a pull request.
	//
	// Linked worktrees share the main repository's .git/config, so a value set
	// once in the main checkout applies from every worktree of that repo.
	//
	// wt.context.* is deliberately not read here — see contextRulesFromGitConfig.
	//
	// The [files] lists are read here, and this is the scope that answers "keep
	// my untracked files coming across, per repo, with nothing committed": it
	// needs no file the repository would carry in a pull request.
	localGit := gitConfigFn(gitScopeLocal)
	applyGitConfig(localGit, "git config (local)")
	accumulateGitConfigFilePatterns(localGit, "git config (local)")

	// 6. Environment variables override every file-based source
	if v := os.Getenv("WORKTREE_ROOT"); v != "" {
		worktreeRoot = v
		configSources.Root = "env: WORKTREE_ROOT"
	}
	if v := os.Getenv("WT_REPO_ROOT"); v != "" {
		reposRoot = expandHome(v)
		configSources.RepoRoot = "env: WT_REPO_ROOT"
	}
	if v := os.Getenv("WT_REPO_PATTERN"); v != "" {
		repoPattern = strings.TrimSpace(v)
		configSources.RepoPattern = "env: WT_REPO_PATTERN"
	}
	if v := os.Getenv("WORKTREE_STRATEGY"); v != "" {
		worktreeStrategy = strings.ToLower(strings.TrimSpace(v))
		configSources.Strategy = "env: WORKTREE_STRATEGY"
	}
	if v := os.Getenv("WORKTREE_PATTERN"); v != "" {
		worktreePattern = strings.TrimSpace(v)
		configSources.Pattern = "env: WORKTREE_PATTERN"
	}
	if v, ok := os.LookupEnv("WORKTREE_SEPARATOR"); ok {
		worktreeSeparator = v
		configSources.Separator = "env: WORKTREE_SEPARATOR"
	}
	if v, ok := os.LookupEnv("WT_COPY_IGNORED"); ok {
		if b, ok := parseGitBool(v); ok {
			filesCopyIgnored = b
			configSources.CopyIgnored = "env: WT_COPY_IGNORED"
		}
	}
}

// expandHome replaces a leading ~ with the user's home directory
// and expands environment variables ($VAR, ${VAR}, and %VAR% on Windows).
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		path = filepath.Join(home, path[1:])
	}
	expanded := os.ExpandEnv(path)
	if runtime.GOOS == "windows" {
		expanded = expandWindowsEnv(expanded)
	}
	return expanded
}

// expandWindowsEnv expands %VAR% style environment variables.
func expandWindowsEnv(path string) string {
	for {
		start := strings.Index(path, "%")
		if start == -1 {
			break
		}
		end := strings.Index(path[start+1:], "%")
		if end == -1 {
			break
		}
		end += start + 1
		varName := path[start+1 : end]
		if val, ok := os.LookupEnv(varName); ok {
			path = path[:start] + val + path[end+1:]
		} else {
			path = path[:start] + path[end+1:]
		}
	}
	return path
}

// writeDefaultConfig creates a default config file at the given path.
func writeDefaultConfig(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config file already exists: %s (use --force to overwrite)", path)
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(defaultConfigTemplate), 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

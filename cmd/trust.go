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
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

// Trust records which repo-supplied hook configurations the user has approved.
//
// `.wt.toml` lives in the working tree, so it is committed and travels with the
// repository: cloning an untrusted repo and running `wt create` would otherwise
// execute whatever that repo put in its [hooks] table (issue #129). git's own
// hooks are deliberately not transferred on clone for the same reason, and
// direnv requires `direnv allow` before it will source a committed .envrc.
//
// A record is (repo, sha256) and BOTH must match before the repo's hooks run:
//
//   - Hashing the file means any edit — a `git pull` that adds a post_create,
//     a branch whose .wt.toml differs — invalidates the approval and asks
//     again. Trusting a path once and forever would let a later commit walk
//     straight in.
//   - Pinning the repo as well means an attacker cannot get a free pass by
//     shipping a .wt.toml byte-identical to one you already approved elsewhere:
//     `make setup` is only as safe as the Makefile sitting next to it.
//
// The store lives in wt's own config directory, never in the repository and
// never in .git/config: a repo handed to you as a directory rather than a clone
// owns its .git/config too.

// trustRecord is a single approved repo-level hook configuration.
type trustRecord struct {
	Repo       string `toml:"repo"`
	File       string `toml:"file"`
	SHA256     string `toml:"sha256"`
	ApprovedAt string `toml:"approved_at"`
}

// trustStore is the on-disk set of approvals.
type trustStore struct {
	Trusted []trustRecord `toml:"trusted"`
}

// trustFilePath is the location of the trust store.
func trustFilePath() string {
	return filepath.Join(configDir(), "trust.toml")
}

// loadTrustStore reads the trust store. A missing store is not an error: it
// simply means nothing has been approved yet. A malformed one *is* an error —
// unlike the config file, silently treating unreadable trust as "no trust" is
// the safe direction, but staying quiet about it would leave the user
// re-approving forever without knowing why.
func loadTrustStore() (trustStore, error) {
	var store trustStore
	path := trustFilePath()
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
	return store, nil
}

// saveTrustStore writes the trust store, replacing any existing one.
//
// Written via a temp file and rename so an interrupted write cannot leave a
// truncated store behind — which would silently revoke every approval.
func saveTrustStore(store trustStore) error {
	path := trustFilePath()
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
# Each entry records a repo-level .wt.toml whose [hooks] you approved, pinned to
# the file's contents. Editing that .wt.toml invalidates the entry and wt will
# ask again. Deleting this file revokes every approval.

`

// isTrusted reports whether this exact file content has been approved for this
// exact repo.
func (s trustStore) isTrusted(repo, sha string) bool {
	if repo == "" || sha == "" {
		return false
	}
	for _, rec := range s.Trusted {
		if rec.Repo == repo && rec.SHA256 == sha {
			return true
		}
	}
	return false
}

// add records an approval, replacing any previous entry with the same repo and
// hash. Entries for the same repo with a *different* hash are kept: worktrees of
// one repo can legitimately sit on branches whose .wt.toml differ, and dropping
// them would make switching between two approved branches re-prompt each time.
func (s *trustStore) add(rec trustRecord) {
	for i, existing := range s.Trusted {
		if existing.Repo == rec.Repo && existing.SHA256 == rec.SHA256 {
			s.Trusted[i] = rec
			return
		}
	}
	s.Trusted = append(s.Trusted, rec)
}

// remove drops every approval for a repo and reports how many went.
func (s *trustStore) remove(repo string) int {
	kept := make([]trustRecord, 0, len(s.Trusted))
	for _, rec := range s.Trusted {
		if rec.Repo != repo {
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

// hashFile returns the hex sha256 of a file's contents.
//
// Only for reporting on files wt is not about to act on (wt trust --list).
// Anything gating execution is pinned to the bytes that were actually decoded —
// see configRepoSHA.
func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
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

// repoHookTrust describes the trust state of the current repo's .wt.toml.
type repoHookTrust struct {
	repo    string // git common dir, the identity trust is pinned to
	file    string // path to the .wt.toml
	sha     string // sha256 of its contents
	trusted bool
}

// currentRepoHookTrust resolves the trust state of the repo-level config that
// supplied the hooks about to run.
func currentRepoHookTrust() (repoHookTrust, error) {
	if configRepoPath == "" || !configRepoFound {
		return repoHookTrust{}, fmt.Errorf("no repo-level .wt.toml loaded")
	}
	// The identity resolved at config load. Falling back to resolving it now
	// covers callers that set the path globals directly, but the cached value is
	// the one to prefer: see configRepoKey.
	repo := configRepoKey
	if repo == "" {
		var err error
		repo, err = repoTrustKeyFn()
		if err != nil {
			return repoHookTrust{}, err
		}
	}
	// The hash of the bytes loadWorktreeConfig decoded, not a fresh read of the
	// path: approval must cover the commands wt is actually holding.
	sha := configRepoSHA
	if sha == "" {
		return repoHookTrust{}, fmt.Errorf("no hash recorded for %s", configRepoPath)
	}
	store, err := loadTrustStore()
	if err != nil {
		return repoHookTrust{}, err
	}
	return repoHookTrust{
		repo:    repo,
		file:    configRepoPath,
		sha:     sha,
		trusted: store.isTrusted(repo, sha),
	}, nil
}

// trustCurrentRepo records an approval for the current repo's .wt.toml.
func trustCurrentRepo(t repoHookTrust) error {
	store, err := loadTrustStore()
	if err != nil {
		return err
	}
	store.add(trustRecord{
		Repo:       t.repo,
		File:       t.file,
		SHA256:     t.sha,
		ApprovedAt: time.Now().UTC().Format(time.RFC3339),
	})
	return saveTrustStore(store)
}

var trustList bool

var trustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Approve the current repository's .wt.toml hooks",
	Long: `Approve the hooks in this repository's .wt.toml.

.wt.toml is committed, so its [hooks] table is supplied by whoever wrote the
repository rather than by you. wt will not run those commands until you approve
them, and the approval is pinned to the file's current contents: if .wt.toml
changes, wt asks again.

  wt trust           approve this repository's .wt.toml
  wt trust --list    show every approval on this machine
  wt untrust         revoke approval for this repository`,
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
	Short: "Revoke approval for the current repository's .wt.toml hooks",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUntrust(cmd)
	},
}

func init() {
	trustCmd.Flags().BoolVar(&trustList, "list", false, "List all approved .wt.toml files")
}

func runTrust(cmd *cobra.Command) error {
	if configRepoPath == "" {
		return fmt.Errorf("not in a git repository")
	}
	if !configRepoFound {
		return fmt.Errorf("no .wt.toml found at %s", configRepoPath)
	}

	t, err := currentRepoHookTrust()
	if err != nil {
		return err
	}

	if t.trusted {
		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{"file": t.file, "sha256": t.sha, "trusted": true, "changed": false})
		}
		fmt.Printf("Already trusted: %s\n", t.file)
		return nil
	}

	// Show what is being approved. Trusting without reading is the failure mode
	// this whole mechanism exists to prevent, so put the commands on screen even
	// when the user asked for it explicitly.
	if !isJSONOutput() {
		// No event is firing: wt trust approves the file, not a run.
		printHookApprovalRequest(os.Stdout, repoHookCommands(), "", t)
	}

	if err := trustCurrentRepo(t); err != nil {
		return err
	}

	if isJSONOutput() {
		return emitJSONSuccess(cmd, map[string]any{"file": t.file, "sha256": t.sha, "trusted": true, "changed": true})
	}
	fmt.Printf("Trusted: %s\n", t.file)
	fmt.Printf("  wt will ask again if this file changes.\n")
	return nil
}

func runUntrust(cmd *cobra.Command) error {
	repo, err := repoTrustKeyFn()
	if err != nil {
		return err
	}
	store, err := loadTrustStore()
	if err != nil {
		return err
	}
	removed := store.remove(repo)
	if removed > 0 {
		if err := saveTrustStore(store); err != nil {
			return err
		}
	}

	if isJSONOutput() {
		return emitJSONSuccess(cmd, map[string]any{"repo": repo, "removed": removed})
	}
	if removed == 0 {
		fmt.Println("Nothing to revoke: this repository has no approved .wt.toml")
		return nil
	}
	fmt.Printf("Revoked %d approval(s) for %s\n", removed, repo)
	return nil
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
				"repo":        rec.Repo,
				"file":        rec.File,
				"sha256":      rec.SHA256,
				"approved_at": rec.ApprovedAt,
				"current":     trustRecordCurrent(rec),
			})
		}
		return emitJSONSuccess(cmd, map[string]any{"trust_file": trustFilePath(), "trusted": records})
	}

	if len(store.Trusted) == 0 {
		fmt.Println("No approved .wt.toml files.")
		return nil
	}

	fmt.Printf("Trust store: %s\n\n", trustFilePath())
	for _, rec := range store.Trusted {
		marker := ""
		if !trustRecordCurrent(rec) {
			// Kept rather than pruned: the branch that approved it may simply not
			// be checked out right now.
			marker = "  (file changed or gone since approval)"
		}
		fmt.Printf("  %s%s\n", rec.File, marker)
		fmt.Printf("    repo:     %s\n", rec.Repo)
		fmt.Printf("    sha256:   %s\n", rec.SHA256)
		fmt.Printf("    approved: %s\n\n", rec.ApprovedAt)
	}
	return nil
}

// trustRecordCurrent reports whether the file a record points at still hashes to
// the approved value.
func trustRecordCurrent(rec trustRecord) bool {
	sha, err := hashFile(rec.File)
	return err == nil && sha == rec.SHA256
}

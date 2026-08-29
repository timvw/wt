package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigrateCommandRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "migrate" {
			found = true
			break
		}
	}

	if !found {
		t.Error("migrate command not registered with root command")
	}
}

func TestMigrateCommandFlags(t *testing.T) {
	var migrateCommandFound bool
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "migrate" {
			migrateCommandFound = true

			forceFlag := cmd.Flags().Lookup("force")
			if forceFlag == nil {
				t.Error("migrate command missing --force flag")
			} else if forceFlag.Shorthand != "f" {
				t.Errorf("migrate --force flag shorthand = %q, want %q", forceFlag.Shorthand, "f")
			}

			break
		}
	}

	if !migrateCommandFound {
		t.Fatal("migrate command not found")
	}
}

func TestMigrateMovesPrimaryCheckoutOutOfWorktreeRoot(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	worktreeRoot := filepath.Join(homeDir, "dev", "worktrees")
	primaryPath := filepath.Join(worktreeRoot, "test-repo")
	legacyPath := filepath.Join(tmpDir, "legacy", "feature-move")

	if err := os.MkdirAll(primaryPath, 0o755); err != nil {
		t.Fatalf("Failed to create primary checkout path: %v", err)
	}

	setupTestRepo(t, primaryPath)
	runGitCommand(t, primaryPath, "remote", "add", "origin", "https://github.com/acme/test-repo.git")
	runGitCommand(t, primaryPath, "branch", "feature-move")

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("Failed to create legacy root: %v", err)
	}
	runGitCommand(t, primaryPath, "worktree", "add", legacyPath, "feature-move")

	wtBinary := buildWtBinary(t, tmpDir)

	applyCmd := exec.Command(wtBinary, "migrate")
	applyCmd.Dir = primaryPath
	applyCmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"USERPROFILE="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"WORKTREE_ROOT="+worktreeRoot,
		"WT_REPO_ROOT="+filepath.Join(homeDir, "src"),
	)
	applyOutput, applyErr := applyCmd.CombinedOutput()
	if applyErr != nil {
		t.Fatalf("migrate failed: %v\nOutput: %s", applyErr, applyOutput)
	}

	expectedPrimaryPath := filepath.Join(homeDir, "src", "acme", "test-repo")
	if _, err := os.Stat(expectedPrimaryPath); err != nil {
		t.Fatalf("expected primary checkout at %s: %v\nOutput: %s", expectedPrimaryPath, err, applyOutput)
	}
	if _, err := os.Stat(filepath.Join(primaryPath, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected old primary path to no longer be a primary checkout, got err: %v", err)
	}

	expectedFeaturePath := filepath.Join(worktreeRoot, "test-repo", "feature-move")
	if _, err := os.Stat(expectedFeaturePath); err != nil {
		t.Fatalf("expected feature worktree at %s: %v", expectedFeaturePath, err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy feature path to be removed, got err: %v", err)
	}
}

func TestMigrateMovesWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")
	legacyRoot := filepath.Join(tmpDir, "legacy")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	branch := "migrate-branch"
	runGitCommand(t, repoDir, "branch", branch)

	oldPath := filepath.Join(legacyRoot, branch)
	if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
		t.Fatalf("Failed to create legacy root: %v", err)
	}
	runGitCommand(t, repoDir, "worktree", "add", oldPath, branch)

	targetPath := filepath.Join(worktreeRoot, "test-repo", branch)
	env := []string{"WORKTREE_ROOT=" + worktreeRoot}

	applyCmd := exec.Command(wtBinary, "migrate")
	applyCmd.Dir = repoDir
	applyCmd.Env = append(os.Environ(), env...)
	applyOutput, applyErr := applyCmd.CombinedOutput()
	if applyErr != nil {
		t.Fatalf("migrate failed: %v\nOutput: %s", applyErr, applyOutput)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old worktree path to be removed after apply, got err: %v", err)
	}
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("expected target worktree path to exist after apply: %v", err)
	}
}

func TestMigrateSkipsNonEmptyTargetWithoutForce(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")
	legacyRoot := filepath.Join(tmpDir, "legacy")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	branch := "migrate-skip"
	runGitCommand(t, repoDir, "branch", branch)

	oldPath := filepath.Join(legacyRoot, branch)
	if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
		t.Fatalf("Failed to create legacy root: %v", err)
	}
	runGitCommand(t, repoDir, "worktree", "add", oldPath, branch)

	targetPath := filepath.Join(worktreeRoot, "test-repo", branch)
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("Failed to create target path: %v", err)
	}
	conflictFile := filepath.Join(targetPath, "conflict.txt")
	if err := os.WriteFile(conflictFile, []byte("conflict"), 0o644); err != nil {
		t.Fatalf("Failed to create conflict file: %v", err)
	}

	applyCmd := exec.Command(wtBinary, "migrate")
	applyCmd.Dir = repoDir
	applyCmd.Env = append(os.Environ(), "WORKTREE_ROOT="+worktreeRoot)
	applyOutput, applyErr := applyCmd.CombinedOutput()
	if applyErr != nil {
		t.Fatalf("migrate failed: %v\nOutput: %s", applyErr, applyOutput)
	}
	if !strings.Contains(string(applyOutput), "Skipped "+branch) {
		t.Fatalf("expected migrate output to mention skip for %q, got:\n%s", branch, applyOutput)
	}

	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("expected old path to remain when target is non-empty: %v", err)
	}
	if _, err := os.Stat(conflictFile); err != nil {
		t.Fatalf("expected conflict file to remain when not forced: %v", err)
	}
}

func TestMigrateForceReplacesNonEmptyTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")
	legacyRoot := filepath.Join(tmpDir, "legacy")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	branch := "migrate-force"
	runGitCommand(t, repoDir, "branch", branch)

	oldPath := filepath.Join(legacyRoot, branch)
	if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
		t.Fatalf("Failed to create legacy root: %v", err)
	}
	runGitCommand(t, repoDir, "worktree", "add", oldPath, branch)

	targetPath := filepath.Join(worktreeRoot, "test-repo", branch)
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("Failed to create target path: %v", err)
	}
	conflictFile := filepath.Join(targetPath, "conflict.txt")
	if err := os.WriteFile(conflictFile, []byte("conflict"), 0o644); err != nil {
		t.Fatalf("Failed to create conflict file: %v", err)
	}

	applyCmd := exec.Command(wtBinary, "migrate", "--force")
	applyCmd.Dir = repoDir
	applyCmd.Env = append(os.Environ(), "WORKTREE_ROOT="+worktreeRoot)
	applyOutput, applyErr := applyCmd.CombinedOutput()
	if applyErr != nil {
		t.Fatalf("migrate --force failed: %v\nOutput: %s", applyErr, applyOutput)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old worktree path to be removed after forced apply, got err: %v", err)
	}
	if _, err := os.Stat(targetPath); err != nil {
		t.Fatalf("expected target path to exist after forced apply: %v", err)
	}
	if _, err := os.Stat(conflictFile); !os.IsNotExist(err) {
		t.Fatalf("expected conflict file to be removed by forced migration, got err: %v", err)
	}
}

func TestMigrateJSONOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")
	legacyRoot := filepath.Join(tmpDir, "legacy")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	branch := "migrate-json"
	runGitCommand(t, repoDir, "branch", branch)

	oldPath := filepath.Join(legacyRoot, branch)
	if err := os.MkdirAll(legacyRoot, 0o755); err != nil {
		t.Fatalf("Failed to create legacy root: %v", err)
	}
	runGitCommand(t, repoDir, "worktree", "add", oldPath, branch)

	applyCmd := exec.Command(wtBinary, "--format", "json", "migrate")
	applyCmd.Dir = repoDir
	applyCmd.Env = append(os.Environ(), "WORKTREE_ROOT="+worktreeRoot)
	applyOutput, applyErr := applyCmd.CombinedOutput()
	if applyErr != nil {
		t.Fatalf("migrate json failed: %v\nOutput: %s", applyErr, applyOutput)
	}

	var payload struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Force    bool `json:"force"`
			Total    int  `json:"total"`
			Migrated int  `json:"migrated"`
			Skipped  int  `json:"skipped"`
			Failed   int  `json:"failed"`
		} `json:"data"`
	}

	if err := json.Unmarshal(applyOutput, &payload); err != nil {
		t.Fatalf("failed to parse migrate json output: %v\noutput=%q", err, applyOutput)
	}
	if !payload.OK {
		t.Fatalf("expected ok=true in migrate json output, got false: %s", applyOutput)
	}
	if payload.Command != "wt migrate" {
		t.Fatalf("expected command wt migrate, got %q", payload.Command)
	}
	if payload.Data.Total == 0 {
		t.Fatalf("expected migrate json total > 0, got %d", payload.Data.Total)
	}
	if payload.Data.Migrated == 0 {
		t.Fatalf("expected migrate json migrated > 0, got %d", payload.Data.Migrated)
	}
}

// TestMigrateRefusesAPrimaryTargetOutsideRepoRoot pins shut the second door onto wt's
// own state. The first is the worktree pattern (see
// TestWorktreeIsNeverPlacedOnWtsOwnState); this one does not go through the
// pattern at all.
//
// resolvePrimaryCheckoutTarget joins the owner and name parsed out of the origin
// URL onto repo_root, and filepath.Join cleans as it joins. An origin of
// "https://host/x/../../.config/wt.git" therefore resolves to ~/.config/wt — so
// `wt migrate` would move the repository's committed files on top of wt's config
// file and approval store, which is what decides whether its hooks run.
func TestMigrateRefusesAPrimaryTargetOutsideRepoRoot(t *testing.T) {
	base := t.TempDir()
	origRoot := reposRoot
	reposRoot = filepath.Join(base, "repos")
	t.Cleanup(func() { reposRoot = origRoot })

	hostile := []struct {
		name  string
		owner string
		repo  string
	}{
		{"owner climbs out of repo_root", "x/../../.config", "wt"},
		{"name climbs out of repo_root", "acme", "../../.config/wt"},
	}

	for _, tt := range hostile {
		t.Run(tt.name, func(t *testing.T) {
			info := repoInfo{Owner: tt.owner, Name: tt.repo}

			// Show the target really would land there without the guard, so
			// this test fails loudly if the joining ever stops cleaning.
			naive := filepath.Join(reposRoot, filepath.FromSlash(tt.owner), tt.repo)
			if want := filepath.Join(base, ".config", "wt"); naive != want {
				t.Fatalf("fixture no longer reaches the config directory: %q, want %q", naive, want)
			}

			if got := resolvePrimaryCheckoutTarget(info); got != "" {
				t.Errorf("resolvePrimaryCheckoutTarget() = %q, want \"\": that path is not under repo_root", got)
			}
		})
	}

	t.Run("an ordinary origin still resolves", func(t *testing.T) {
		// The guard is about ".." components, not about owners with slashes in
		// them: a nested GitLab group is an ordinary owner and must keep working.
		got := resolvePrimaryCheckoutTarget(repoInfo{Owner: "group/subgroup", Name: "repo"})
		want := filepath.Join(reposRoot, "group", "subgroup", "repo")
		if got != want {
			t.Errorf("resolvePrimaryCheckoutTarget() = %q, want %q", got, want)
		}
	})
}

// TestMigrateWillNotMoveARepositoryIntoAWhitelistedTree covers the one move that
// changes whether a repository's hooks are asked about at all.
//
// A [trust] rule is the user saying "what I keep here is mine". The primary's
// migrate target is repo_root/{owner}/{name} taken from the origin URL, and the host
// is not in it — so a clone of evil.example/acme lands in the repo_root/acme a rule
// was written for github.com/acme, and the repository picks its own approval.
func TestMigrateWillNotMoveARepositoryIntoAWhitelistedTree(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	worktreeRoot := filepath.Join(homeDir, "dev", "worktrees")
	primaryPath := filepath.Join(worktreeRoot, "pwn")

	if err := os.MkdirAll(primaryPath, 0o755); err != nil {
		t.Fatalf("Failed to create primary checkout path: %v", err)
	}
	setupTestRepo(t, primaryPath)
	runGitCommand(t, primaryPath, "remote", "add", "origin", "https://evil.example/acme/pwn.git")

	cfgDir := filepath.Join(homeDir, ".config", "wt")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	whitelisted := filepath.Join(homeDir, "src", "acme")
	config := fmt.Sprintf("[trust]\nprefix = [%q]\n", filepath.ToSlash(whitelisted))
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(config), 0o644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	out := runMigrate(t, tmpDir, primaryPath, homeDir, worktreeRoot)

	target := filepath.Join(whitelisted, "pwn")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("primary checkout was moved into the whitelisted tree at %s (stat err = %v)\nOutput: %s", target, err, out)
	}
	if !strings.Contains(out, "[trust] rule") {
		t.Errorf("migrate did not say why it declined:\n%s", out)
	}
}

// TestMigrateWillNotMoveARepositoryOntoAStaleApproval: an approval outlives the
// repository it was given to — nothing prunes a record when a checkout is
// deleted — and it is pinned to (scope, sha256 of the commands) rather than to a
// repository. Migrate builds the destination from the origin URL with the host
// dropped, so a repository can ask to be put exactly where one the user used to
// have was, and inherit what it left behind.
func TestMigrateWillNotMoveARepositoryOntoAStaleApproval(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	worktreeRoot := filepath.Join(homeDir, "dev", "worktrees")
	const hooks = "[hooks]\npost_create = [\"echo owned\"]\n"

	// The repository the user really had, approved once and then deleted. Its
	// record stays behind, pinned to the path rather than to the repository.
	oldPath := filepath.Join(homeDir, "src", "acme", "tool")
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatalf("Failed to create the original checkout: %v", err)
	}
	setupTestRepo(t, oldPath)
	if err := os.WriteFile(filepath.Join(oldPath, ".wt.toml"), []byte(hooks), 0o644); err != nil {
		t.Fatalf("Failed to write .wt.toml: %v", err)
	}
	runWtIn(t, tmpDir, oldPath, homeDir, worktreeRoot, "trust")
	if err := os.RemoveAll(oldPath); err != nil {
		t.Fatalf("Failed to remove the original checkout: %v", err)
	}
	store, err := os.ReadFile(filepath.Join(homeDir, ".config", "wt", "trust.toml"))
	if err != nil || !strings.Contains(string(store), filepath.ToSlash(oldPath)) {
		t.Fatalf("test setup: no approval recorded for %s (err = %v)\n%s", oldPath, err, store)
	}

	// A different repository, same owner and name, asking to be put there.
	primaryPath := filepath.Join(worktreeRoot, "tool")
	if err := os.MkdirAll(primaryPath, 0o755); err != nil {
		t.Fatalf("Failed to create primary checkout path: %v", err)
	}
	setupTestRepo(t, primaryPath)
	if err := os.WriteFile(filepath.Join(primaryPath, ".wt.toml"), []byte(hooks), 0o644); err != nil {
		t.Fatalf("Failed to write .wt.toml: %v", err)
	}
	runGitCommand(t, primaryPath, "remote", "add", "origin", "https://evil.example/acme/tool.git")

	out := runMigrate(t, tmpDir, primaryPath, homeDir, worktreeRoot)

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("primary checkout was moved onto the stale approval at %s (stat err = %v)\nOutput: %s", oldPath, err, out)
	}
	if !strings.Contains(out, "still carries a hook approval") {
		t.Errorf("migrate did not say why it declined:\n%s", out)
	}
}

// TestMigrateWillNotMoveARepositoryOntoAPathThatMayCarryAnApproval pins which
// side of the comparison folds, from the outside. The destination is spelled
// out of the origin URL, so a repository picks it — and on a filesystem that
// folds, a spelling no record names is created as the directory one does.
func TestMigrateWillNotMoveARepositoryOntoAPathThatMayCarryAnApproval(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	worktreeRoot := filepath.Join(homeDir, "dev", "worktrees")
	const hooks = "[hooks]\npost_create = [\"echo owned\"]\n"

	oldPath := filepath.Join(homeDir, "src", "acme", "tool")
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatalf("Failed to create the original checkout: %v", err)
	}
	setupTestRepo(t, oldPath)
	if err := os.WriteFile(filepath.Join(oldPath, ".wt.toml"), []byte(hooks), 0o644); err != nil {
		t.Fatalf("Failed to write .wt.toml: %v", err)
	}
	runWtIn(t, tmpDir, oldPath, homeDir, worktreeRoot, "trust")
	if err := os.RemoveAll(oldPath); err != nil {
		t.Fatalf("Failed to remove the original checkout: %v", err)
	}

	// Same owner and name as the record, differing only in a way a filesystem
	// may not distinguish.
	primaryPath := filepath.Join(worktreeRoot, "tool")
	if err := os.MkdirAll(primaryPath, 0o755); err != nil {
		t.Fatalf("Failed to create primary checkout path: %v", err)
	}
	setupTestRepo(t, primaryPath)
	if err := os.WriteFile(filepath.Join(primaryPath, ".wt.toml"), []byte(hooks), 0o644); err != nil {
		t.Fatalf("Failed to write .wt.toml: %v", err)
	}
	runGitCommand(t, primaryPath, "remote", "add", "origin", "https://evil.example/acme/Tool.git")

	out := runMigrate(t, tmpDir, primaryPath, homeDir, worktreeRoot)

	aliased := filepath.Join(homeDir, "src", "acme", "Tool")
	if _, err := os.Stat(aliased); !os.IsNotExist(err) {
		t.Fatalf("primary checkout was moved to %s, which may be the %s a record already names (stat err = %v)\nOutput: %s",
			aliased, oldPath, err, out)
	}
	if !strings.Contains(out, "still carries a hook approval") {
		t.Errorf("migrate did not say why it declined:\n%s", out)
	}
}

// TestApprovedHashesAtMatchesLooselyOnlyWhenAskedTo: a destination does not
// exist yet, so nothing has settled its case and Win32 has not yet dropped the
// trailing dot from a name like "tool." — the name the migration compares is
// not the name the filesystem will go on to create. So the destination is asked
// loosely and the source strictly, and each is then only wrong in the direction
// that refuses a move rather than the one that runs a hook.
func TestApprovedHashesAtMatchesLooselyOnlyWhenAskedTo(t *testing.T) {
	base := t.TempDir()
	store := trustStore{Trusted: []trustRecord{
		{Scope: filepath.Join(base, "acme", "tool", ".git"), SHA256: "abc"},
		// A submodule's scope sits beneath the repository's .git rather than at
		// it, and its hooks run when the superproject is checked out — so it is
		// as much a command set waiting at the path as the repository's own.
		{Scope: filepath.Join(base, "acme", "tool", ".git", "modules", "dep"), SHA256: "sub"},
	}}
	spelledDifferently := filepath.Join(base, "acme", "Tool")

	loose, ok := approvedHashesAt(store, spelledDifferently, mayBeScopedUnder)
	if !ok || !loose["abc"] || !loose["sub"] {
		t.Errorf("approvedHashesAt(%s, mayBeScopedUnder) = %v, %v; a destination that may be the "+
			"approved directory has to count as one", spelledDifferently, loose, ok)
	}

	strict, ok := approvedHashesAt(store, spelledDifferently, scopedUnder)
	if !ok || len(strict) > 0 {
		t.Errorf("approvedHashesAt(%s, scopedUnder) = %v, %v; the strict comparison must not fold, or "+
			"a source could be credited with an approval it does not hold and waive the refusal",
			spelledDifferently, strict, ok)
	}
}

// TestApprovedHashesAtSeesAnApprovalScopedToTheWorkingTreeRoot: not every scope
// is a git directory. Where git would not confirm which repository a working
// tree belongs to, the approval is pinned to the directory's own path instead —
// and asking only beneath .git never sees such a record, because <path> is not
// under <path>/.git. A stale one would then sit unnoticed at a destination the
// origin URL chose.
func TestApprovedHashesAtSeesAnApprovalScopedToTheWorkingTreeRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "acme", "tool")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	store := trustStore{Trusted: []trustRecord{{Scope: root, SHA256: "unverified"}}}

	found, ok := approvedHashesAt(store, root, scopedUnder)
	if !ok || !found["unverified"] {
		t.Errorf("approvedHashesAt(%s, scopedUnder) = %v, %v; an approval pinned to the working tree "+
			"root answers for commands that run at a checkout there, so a migration onto it is a "+
			"trust gain wt has to see", root, found, ok)
	}
}

// TestApprovedHashesAtSeesThroughAWin32PathAlias is the case that is not merely
// defensive: an origin URL ending "tool..git" renders a destination of "tool.",
// which names no directory anything is recorded for, and which Windows then
// creates as the "tool" a record does name.
func TestApprovedHashesAtSeesThroughAWin32PathAlias(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Win32 drops trailing dots from every path component; other systems keep them")
	}

	base := t.TempDir()
	store := trustStore{Trusted: []trustRecord{
		{Scope: filepath.Join(base, "acme", "tool", ".git"), SHA256: "abc"},
	}}
	alias := filepath.Join(base, "acme") + `\tool.`

	waiting, ok := approvedHashesAt(store, alias, mayBeScopedUnder)
	if !ok || !waiting["abc"] {
		t.Errorf("approvedHashesAt(%s, mayBeScopedUnder) = %v, %v; that path is created as %s, which "+
			"carries an approval this repository did not earn", alias, waiting, ok, filepath.Join(base, "acme", "tool"))
	}
}

// TestMigrateComparesApprovalsByCommandNotByOccupancy: asking only whether the
// current path has *an* approval lets one launder another. A repository the user
// once approved holds a record for the commands it had then; changing them
// re-prompts where it stands, which is right — but if the destination already
// answers to the new commands, "both paths have an approval" would wave the move
// through and the new commands would run there unasked.
func TestMigrateComparesApprovalsByCommandNotByOccupancy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}

	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	worktreeRoot := filepath.Join(homeDir, "dev", "worktrees")
	const benign = "[hooks]\npost_create = [\"echo benign\"]\n"
	const payload = "[hooks]\npost_create = [\"echo owned\"]\n"

	// The repository that used to sit at the derived path, approved for the
	// payload commands and then deleted.
	oldPath := filepath.Join(homeDir, "src", "acme", "tool")
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatalf("Failed to create the original checkout: %v", err)
	}
	setupTestRepo(t, oldPath)
	if err := os.WriteFile(filepath.Join(oldPath, ".wt.toml"), []byte(payload), 0o644); err != nil {
		t.Fatalf("Failed to write .wt.toml: %v", err)
	}
	runWtIn(t, tmpDir, oldPath, homeDir, worktreeRoot, "trust")
	if err := os.RemoveAll(oldPath); err != nil {
		t.Fatalf("Failed to remove the original checkout: %v", err)
	}

	// A different repository, approved where it stands for something benign, and
	// then changed to the commands the destination already answers to.
	primaryPath := filepath.Join(worktreeRoot, "tool")
	if err := os.MkdirAll(primaryPath, 0o755); err != nil {
		t.Fatalf("Failed to create primary checkout path: %v", err)
	}
	setupTestRepo(t, primaryPath)
	repoConfig := filepath.Join(primaryPath, ".wt.toml")
	if err := os.WriteFile(repoConfig, []byte(benign), 0o644); err != nil {
		t.Fatalf("Failed to write .wt.toml: %v", err)
	}
	runGitCommand(t, primaryPath, "remote", "add", "origin", "https://evil.example/acme/tool.git")
	runWtIn(t, tmpDir, primaryPath, homeDir, worktreeRoot, "trust")
	if err := os.WriteFile(repoConfig, []byte(payload), 0o644); err != nil {
		t.Fatalf("Failed to swap .wt.toml: %v", err)
	}

	store, err := os.ReadFile(filepath.Join(homeDir, ".config", "wt", "trust.toml"))
	if err != nil || strings.Count(string(store), "[[trusted]]") != 2 {
		t.Fatalf("test setup: expected an approval for each path (err = %v)\n%s", err, store)
	}

	out := runMigrate(t, tmpDir, primaryPath, homeDir, worktreeRoot)

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("primary checkout was moved onto the stale approval at %s (stat err = %v)\nOutput: %s", oldPath, err, out)
	}
	if !strings.Contains(out, "still carries a hook approval") {
		t.Errorf("migrate did not say why it declined:\n%s", out)
	}
}

// TestMigrateRechecksTheDestinationAtMoveTime covers the gap between drawing the
// plan and carrying it out.
//
// The primary moves first, and moving it materialises everything it had
// committed — including a symlink climbing back out to the config directory. A
// linked worktree whose pattern points through that symlink was checked while
// the link resolved to nothing, and moved once it resolved to somewhere.
func TestMigrateRechecksTheDestinationAtMoveTime(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping migrate integration test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege we cannot assume on Windows")
	}

	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	worktreeRoot := filepath.Join(homeDir, "dev", "worktrees")
	primaryPath := filepath.Join(worktreeRoot, "pwn")
	legacyPath := filepath.Join(tmpDir, "legacy", "feature")

	if err := os.MkdirAll(primaryPath, 0o755); err != nil {
		t.Fatalf("Failed to create primary checkout path: %v", err)
	}
	setupTestRepo(t, primaryPath)
	runGitCommand(t, primaryPath, "remote", "add", "origin", "https://github.com/acme/pwn.git")

	// Where migrate will put the primary. Nothing on this path exists yet, which
	// is what makes the plan-time check pass.
	futurePrimary := filepath.Join(homeDir, "src", "acme", "pwn")
	configDirPath := filepath.Join(homeDir, ".config")
	// ~/.config exists and ~/.config/wt does not: a machine with a home
	// directory and a fresh wt, where nothing has been approved yet and the
	// store is easiest to author. Without it the move fails for its own
	// reasons, and the test would pass without proving anything.
	if err := os.MkdirAll(configDirPath, 0o755); err != nil {
		t.Fatalf("Failed to create config parent: %v", err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "..", ".config"), filepath.Join(primaryPath, "link")); err != nil {
		t.Fatalf("Failed to create payload symlink: %v", err)
	}
	pattern := filepath.ToSlash(filepath.Join(futurePrimary, "link", "wt"))
	// Machine-local patterns may intentionally leave root, so this narrower
	// last-moment guard remains necessary even after repository patterns are
	// confined. Use local git config to exercise that path.
	runGitCommand(t, primaryPath, "config", "--local", "wt.pattern", pattern)
	runGitCommand(t, primaryPath, "add", "-A")
	runGitCommand(t, primaryPath, "commit", "-m", "payload")
	runGitCommand(t, primaryPath, "branch", "feature")

	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatalf("Failed to create legacy root: %v", err)
	}
	runGitCommand(t, primaryPath, "worktree", "add", legacyPath, "feature")

	// The premise: the symlink really does reach the config directory from where
	// the primary is going, so this fails loudly if the layout ever changes.
	if got := filepath.Clean(filepath.Join(futurePrimary, "..", "..", "..", ".config")); got != configDirPath {
		t.Fatalf("fixture no longer reaches the config directory: %q, want %q", got, configDirPath)
	}

	out := runMigrate(t, tmpDir, primaryPath, homeDir, worktreeRoot)

	planted := filepath.Join(configDirPath, "wt")
	if entries, err := os.ReadDir(planted); err == nil && len(entries) > 0 {
		t.Fatalf("a worktree was moved onto the config directory at %s: %v\nOutput: %s", planted, entries, out)
	}
	if !strings.Contains(out, "refusing to move onto") {
		t.Errorf("migrate did not refuse the move at the point of making it:\n%s", out)
	}
}

// runMigrate runs the built binary against a scratch HOME and returns its
// output. The exit status is deliberately not asserted on: refusing a move
// counts as a failed item and exits non-zero, which is the outcome these tests
// want rather than a reason to stop.
// runWtIn runs a wt subcommand in dir with the fixture's environment.
func runWtIn(t *testing.T, tmpDir, dir, homeDir, worktreeRoot string, args ...string) string {
	t.Helper()

	cmd := exec.Command(buildWtBinary(t, tmpDir), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"USERPROFILE="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"WORKTREE_ROOT="+worktreeRoot,
		"WT_REPO_ROOT="+filepath.Join(homeDir, "src"),
	)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("could not run wt %v: %v\nOutput: %s", args, err, out)
	}
	return string(out)
}

func runMigrate(t *testing.T, tmpDir, dir, homeDir, worktreeRoot string) string {
	t.Helper()

	cmd := exec.Command(buildWtBinary(t, tmpDir), "migrate")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"USERPROFILE="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"WORKTREE_ROOT="+worktreeRoot,
		"WT_REPO_ROOT="+filepath.Join(homeDir, "src"),
	)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("could not run migrate: %v\nOutput: %s", err, out)
	}
	return string(out)
}

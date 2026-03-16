package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
	applyCmd.Env = append(os.Environ(), "HOME="+homeDir, "WORKTREE_ROOT="+worktreeRoot)
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

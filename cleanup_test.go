package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetMergedBranches(t *testing.T) {
	// This test runs in the actual git repository
	// We test against the default base branch

	base := getDefaultBase()
	if base == "" {
		t.Skip("Could not determine default branch, skipping test")
	}

	branches, err := getMergedBranches(base)
	if err != nil {
		// If we're in detached HEAD or can't run the command, skip
		t.Skipf("Could not get merged branches: %v", err)
	}

	// Verify base branch is not included
	for _, branch := range branches {
		if branch == base || branch == "main" || branch == "master" {
			t.Errorf("getMergedBranches() included base branch %q in results", branch)
		}
	}

	// Verify no empty branches
	for _, branch := range branches {
		if strings.TrimSpace(branch) == "" {
			t.Error("getMergedBranches() returned empty branch name")
		}
	}
}

func TestGetMergedBranchesFiltersBaseBranches(t *testing.T) {
	// Create a temporary git repo to test branch filtering
	tmpDir, err := os.MkdirTemp("", "wt-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Initialize git repo
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "Initial commit"},
		{"git", "branch", "-M", "main"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmpDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Failed to run %v: %v\n%s", args, err, out)
		}
	}

	// Save current dir and change to temp repo
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	// Test that main is filtered out
	branches, err := getMergedBranches("main")
	if err != nil {
		t.Fatalf("getMergedBranches failed: %v", err)
	}

	for _, b := range branches {
		if b == "main" || b == "master" {
			t.Errorf("getMergedBranches should filter out %q", b)
		}
	}
}

func TestCleanupCommandFlags(t *testing.T) {
	// Test that the cleanup command has the expected flags
	cmd := cleanupCmd

	dryRunFlag := cmd.Flags().Lookup("dry-run")
	if dryRunFlag == nil {
		t.Error("cleanup command missing --dry-run flag")
	}

	forceFlag := cmd.Flags().Lookup("force")
	if forceFlag == nil {
		t.Error("cleanup command missing --force flag")
	}

	// Check shorthand for force
	if forceFlag != nil && forceFlag.Shorthand != "f" {
		t.Errorf("cleanup --force flag shorthand = %q, want %q", forceFlag.Shorthand, "f")
	}
}

func TestCleanupCommandRegistered(t *testing.T) {
	// Verify the cleanup command is registered with the root command
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Name() == "cleanup" {
			found = true
			break
		}
	}
	if !found {
		t.Error("cleanup command not registered with root command")
	}
}

func TestCleanupE2E(t *testing.T) {
	// Skip if not in a git repo with worktree support
	if _, err := exec.Command("git", "rev-parse", "--git-dir").Output(); err != nil {
		t.Skip("Not in a git repository, skipping E2E test")
	}

	// Create a temporary directory for our test worktree root
	tmpRoot, err := os.MkdirTemp("", "wt-cleanup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpRoot)

	// Create a temporary git repo for isolated testing
	repoDir := filepath.Join(tmpRoot, "repo")
	worktreeDir := filepath.Join(tmpRoot, "worktrees")
	os.MkdirAll(repoDir, 0755)
	os.MkdirAll(worktreeDir, 0755)

	// Initialize a test git repo
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@test.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "commit", "--allow-empty", "-m", "Initial commit"},
		{"git", "branch", "-M", "main"},
		// Create a branch that will be "merged"
		{"git", "checkout", "-b", "feature-merged"},
		{"git", "checkout", "main"},
	}

	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Failed to run %v: %v\n%s", args, err, out)
		}
	}

	// Create a worktree for the merged branch
	wtPath := filepath.Join(worktreeDir, "feature-merged")
	cmd := exec.Command("git", "worktree", "add", wtPath, "feature-merged")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to create test worktree: %v\n%s", err, out)
	}

	// Verify the worktree was created
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Fatal("Test worktree was not created")
	}

	// Save current dir and change to test repo
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(repoDir)

	// Test dry-run mode (should not remove anything)
	cleanupDryRun = true
	cleanupForce = false
	err = cleanupCmd.RunE(cleanupCmd, []string{})
	cleanupDryRun = false

	if err != nil {
		t.Errorf("cleanup --dry-run failed: %v", err)
	}

	// Verify worktree still exists after dry-run
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Error("Worktree was removed during dry-run (should not happen)")
	}

	// Test force mode (should remove without prompting)
	cleanupForce = true
	err = cleanupCmd.RunE(cleanupCmd, []string{})
	cleanupForce = false

	if err != nil {
		t.Errorf("cleanup --force failed: %v", err)
	}

	// Verify worktree was removed
	cmd = exec.Command("git", "worktree", "list")
	cmd.Dir = repoDir
	output, _ := cmd.Output()
	if strings.Contains(string(output), "feature-merged") {
		t.Error("Worktree was not removed after cleanup --force")
	}
}

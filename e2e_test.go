package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2EAutoCdWithNonInteractiveCommand tests that auto-cd works
// when providing a branch name directly (non-interactive mode)
func TestE2EAutoCdWithNonInteractiveCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	// Setup: Create a temporary test environment
	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	// Initialize a git repo
	setupTestRepo(t, repoDir)

	// Build wt binary
	wtBinary := buildWtBinary(t, tmpDir)

	// Create a test branch
	runGitCommand(t, repoDir, "checkout", "-b", "test-branch")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit")
	runGitCommand(t, repoDir, "checkout", "main")

	// Test: Run wt checkout with the shell function in bash
	script := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(wt shellenv)

# Run wt checkout (non-interactive)
wt checkout test-branch

# Print current directory
pwd
`, worktreeRoot, filepath.Dir(wtBinary), repoDir)

	cmd := exec.Command("bash", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run e2e test: %v\nOutput: %s", err, output)
	}

	// Verify: Check that we're in the worktree directory
	expectedPath := filepath.Join(worktreeRoot, "test-repo", "test-branch")
	if !strings.Contains(string(output), expectedPath) {
		t.Errorf("E2E FAIL: Auto-cd didn't work!\nExpected to be in: %s\nOutput: %s",
			expectedPath, output)
	} else {
		t.Logf("E2E PASS: Successfully auto-cd'd to worktree: %s", expectedPath)
	}
}

// TestE2EAutoCdWithCreate tests that auto-cd works when creating a new branch
func TestE2EAutoCdWithCreate(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	script := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(wt shellenv)

# Run wt create
wt create new-feature

# Print current directory
pwd
`, worktreeRoot, filepath.Dir(wtBinary), repoDir)

	cmd := exec.Command("bash", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run e2e test: %v\nOutput: %s", err, output)
	}

	expectedPath := filepath.Join(worktreeRoot, "test-repo", "new-feature")
	if !strings.Contains(string(output), expectedPath) {
		t.Errorf("E2E FAIL: Auto-cd didn't work for create!\nExpected to be in: %s\nOutput: %s",
			expectedPath, output)
	} else {
		t.Logf("E2E PASS: Successfully auto-cd'd to new worktree: %s", expectedPath)
	}
}

// TestE2EAutoCdInZsh tests that auto-cd works in zsh
func TestE2EAutoCdInZsh(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	// Check if zsh is available
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available, skipping zsh e2e test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create a test branch
	runGitCommand(t, repoDir, "checkout", "-b", "zsh-test-branch")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit")
	runGitCommand(t, repoDir, "checkout", "main")

	script := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(wt shellenv)

# Run wt checkout
wt checkout zsh-test-branch

# Print current directory
pwd
`, worktreeRoot, filepath.Dir(wtBinary), repoDir)

	cmd := exec.Command("zsh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run zsh e2e test: %v\nOutput: %s", err, output)
	}

	expectedPath := filepath.Join(worktreeRoot, "test-repo", "zsh-test-branch")
	if !strings.Contains(string(output), expectedPath) {
		t.Errorf("E2E FAIL: Auto-cd didn't work in zsh!\nExpected to be in: %s\nOutput: %s",
			expectedPath, output)
	} else {
		t.Logf("E2E PASS: Successfully auto-cd'd in zsh: %s", expectedPath)
	}
}

// TestE2ERemoveAndAutoCdToMain tests that removing a worktree while in it
// automatically navigates back to the main worktree
//
// NOTE: This test documents a known limitation - the auto-cd after remove
// doesn't always work due to path resolution issues (symlinks, /private/ on macOS)
func TestE2ERemoveAndAutoCdToMain(t *testing.T) {
	t.Skip("Known issue: Auto-cd after remove doesn't work reliably due to path resolution issues. See beads-oss-tasks-y6r")

	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create a test branch first
	runGitCommand(t, repoDir, "checkout", "-b", "temp-branch")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit")
	runGitCommand(t, repoDir, "checkout", "main")

	script := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(wt shellenv)

# Create and cd to worktree
wt checkout temp-branch

# Verify we're in the worktree
echo "After checkout:"
pwd

# Remove the worktree (should auto-cd back to main)
wt remove temp-branch

# Print current directory (should be back at main repo)
echo "After remove:"
pwd
`, worktreeRoot, filepath.Dir(wtBinary), repoDir)

	cmd := exec.Command("bash", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run e2e test: %v\nOutput: %s", err, output)
	}

	outputStr := string(output)

	// Should have been in the worktree first
	worktreePath := filepath.Join(worktreeRoot, "test-repo", "temp-branch")
	if !strings.Contains(outputStr, worktreePath) {
		t.Errorf("E2E: Should have cd'd to worktree first")
	}

	// Then should be back at main repo after remove
	if !strings.Contains(outputStr, repoDir) {
		t.Errorf("E2E FAIL: Didn't auto-cd back to main repo after remove!\nExpected to be in: %s\nOutput: %s",
			repoDir, outputStr)
	} else {
		t.Logf("E2E PASS: Successfully auto-cd'd back to main repo after remove")
	}
}

// Helper functions

func setupTestRepo(t *testing.T, repoDir string) {
	t.Helper()

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("Failed to create repo dir: %v", err)
	}

	// Initialize git repo
	runGitCommand(t, repoDir, "init")
	runGitCommand(t, repoDir, "config", "user.email", "test@example.com")
	runGitCommand(t, repoDir, "config", "user.name", "Test User")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "initial commit")
	runGitCommand(t, repoDir, "branch", "-M", "main")
}

func buildWtBinary(t *testing.T, tmpDir string) string {
	t.Helper()

	binaryName := "wt"
	// On Windows, executables need .exe extension
	if filepath.Separator == '\\' {
		binaryName = "wt.exe"
	}

	binaryPath := filepath.Join(tmpDir, binaryName)
	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build wt binary: %v\nOutput: %s", err, output)
	}

	return binaryPath
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Git command failed: git %v\nError: %v\nOutput: %s",
			args, err, output)
	}
}

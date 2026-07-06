package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRemoveCleansUpResidualWorktreeDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping cleanup test in short mode")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Cleanup test relies on bash wrapper not available on Windows")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("Failed to locate git binary: %v", err)
	}

	// Resolve bash from PATH rather than hardcoding /usr/bin/env, which is absent
	// in sandboxed environments (e.g. the Nix build sandbox).
	realBash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("Skipping cleanup test: bash not available: %v", err)
	}

	fakeGitDir := t.TempDir()
	fakeGitPath := filepath.Join(fakeGitDir, "git")
	gitWrapper := fmt.Sprintf(`#!%s
set -e

REAL_GIT=%q

if [ "$1" = "worktree" ] && [ "$2" = "remove" ]; then
  target="${!#}"
  "$REAL_GIT" "$@"
  mkdir -p "$target"
  exit 0
fi

exec "$REAL_GIT" "$@"`, realBash, realGit)

	if err := os.WriteFile(fakeGitPath, []byte(gitWrapper), 0o755); err != nil {
		t.Fatalf("Failed to write fake git binary: %v", err)
	}
	if err := os.Chmod(fakeGitPath, 0o755); err != nil {
		t.Fatalf("Failed to make fake git executable: %v", err)
	}

	t.Setenv("PATH", fakeGitDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WORKTREE_ROOT", worktreeRoot)

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	runGitCommand(t, repoDir, "branch", "cleanup-branch")

	checkoutCmd := exec.Command(wtBinary, "checkout", "cleanup-branch")
	checkoutCmd.Dir = repoDir
	if output, err := checkoutCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to create worktree: %v\nOutput: %s", err, output)
	}

	worktreePath := filepath.Join(worktreeRoot, "test-repo", "cleanup-branch")
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("Expected worktree path to exist after checkout, got: %v", err)
	}

	removeCmd := exec.Command(wtBinary, "remove", "cleanup-branch")
	removeCmd.Dir = repoDir
	if output, err := removeCmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to remove worktree: %v\nOutput: %s", err, output)
	}

	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("Expected worktree path to be removed, got err: %v", err)
	}
}

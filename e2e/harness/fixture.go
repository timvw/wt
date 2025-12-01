package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Fixture represents a test environment with a temporary git repository
type Fixture struct {
	t            *testing.T
	TempDir      string
	RepoDir      string
	RepoName     string
	WorktreeRoot string
	WtBinary     string
}

// NewFixture creates a new test fixture with a temporary git repository
func NewFixture(t *testing.T, wtBinary string) (*Fixture, error) {
	t.Helper()

	tmpDir := t.TempDir()
	repoName := "test-repo"
	repoDir := filepath.Join(tmpDir, repoName)
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	f := &Fixture{
		t:            t,
		TempDir:      tmpDir,
		RepoDir:      repoDir,
		RepoName:     repoName,
		WorktreeRoot: worktreeRoot,
		WtBinary:     wtBinary,
	}

	if err := f.initRepo(); err != nil {
		return nil, fmt.Errorf("failed to initialize repo: %w", err)
	}

	return f, nil
}

// initRepo initializes a basic git repository with a main branch
func (f *Fixture) initRepo() error {
	if err := os.MkdirAll(f.RepoDir, 0755); err != nil {
		return fmt.Errorf("failed to create repo dir: %w", err)
	}

	commands := [][]string{
		{"init"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test User"},
		{"commit", "--allow-empty", "-m", "initial commit"},
		{"branch", "-M", "main"},
	}

	for _, args := range commands {
		if err := f.runGitCommand(args...); err != nil {
			return fmt.Errorf("git %v failed: %w", args, err)
		}
	}

	return nil
}

// CreateBranch creates a new branch with an empty commit
func (f *Fixture) CreateBranch(branchName, base string) error {
	commands := [][]string{
		{"checkout", base},
		{"checkout", "-b", branchName},
		{"commit", "--allow-empty", "-m", fmt.Sprintf("commit on %s", branchName)},
		{"checkout", base},
	}

	for _, args := range commands {
		if err := f.runGitCommand(args...); err != nil {
			return fmt.Errorf("failed to create branch %s: %w", branchName, err)
		}
	}

	return nil
}

// CreatePRRef creates a GitHub-style PR ref (refs/pull/123/head)
func (f *Fixture) CreatePRRef(prNumber int, branchName string) error {
	refName := fmt.Sprintf("refs/pull/%d/head", prNumber)

	// Get the commit SHA of the branch
	cmd := exec.Command("git", "rev-parse", branchName)
	cmd.Dir = f.RepoDir
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get commit SHA: %w", err)
	}
	sha := string(output[:len(output)-1]) // trim newline

	// Create the ref
	if err := f.runGitCommand("update-ref", refName, sha); err != nil {
		return fmt.Errorf("failed to create PR ref: %w", err)
	}

	return nil
}

// CreateMRRef creates a GitLab-style MR ref (refs/merge-requests/456/head)
func (f *Fixture) CreateMRRef(mrNumber int, branchName string) error {
	refName := fmt.Sprintf("refs/merge-requests/%d/head", mrNumber)

	// Get the commit SHA of the branch
	cmd := exec.Command("git", "rev-parse", branchName)
	cmd.Dir = f.RepoDir
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to get commit SHA: %w", err)
	}
	sha := string(output[:len(output)-1]) // trim newline

	// Create the ref
	if err := f.runGitCommand("update-ref", refName, sha); err != nil {
		return fmt.Errorf("failed to create MR ref: %w", err)
	}

	return nil
}

// runGitCommand executes a git command in the repo directory
func (f *Fixture) runGitCommand(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = f.RepoDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %v failed: %w\nOutput: %s", args, err, output)
	}
	return nil
}

// Cleanup removes the temporary directories (called automatically by t.TempDir())
func (f *Fixture) Cleanup() {
	// TempDir cleanup is automatic, but we can add explicit cleanup if needed
}

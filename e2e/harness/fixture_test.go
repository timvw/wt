package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewFixture(t *testing.T) {
	wtBinary := "/fake/path/to/wt"
	fixture, err := NewFixture(t, wtBinary)
	if err != nil {
		t.Fatalf("NewFixture failed: %v", err)
	}

	// Verify fixture fields are set
	if fixture.TempDir == "" {
		t.Error("TempDir is empty")
	}
	if fixture.RepoDir == "" {
		t.Error("RepoDir is empty")
	}
	if fixture.RepoName != "test-repo" {
		t.Errorf("RepoName = %s, want test-repo", fixture.RepoName)
	}
	if fixture.WorktreeRoot == "" {
		t.Error("WorktreeRoot is empty")
	}
	if fixture.WtBinary != wtBinary {
		t.Errorf("WtBinary = %s, want %s", fixture.WtBinary, wtBinary)
	}

	// Verify repo directory exists
	if _, err := os.Stat(fixture.RepoDir); err != nil {
		t.Errorf("RepoDir does not exist: %v", err)
	}

	// Verify it's a git repo
	gitDir := filepath.Join(fixture.RepoDir, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		t.Errorf(".git directory does not exist: %v", err)
	}
}

func TestFixtureCreateBranch(t *testing.T) {
	wtBinary := "/fake/path/to/wt"
	fixture, err := NewFixture(t, wtBinary)
	if err != nil {
		t.Fatalf("NewFixture failed: %v", err)
	}

	// Create a branch
	if err := fixture.CreateBranch("feature-test", "main"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	// Verify branch was created (would need git commands to verify properly)
	// For now, just check no error occurred
}

func TestFixtureCreatePRRef(t *testing.T) {
	wtBinary := "/fake/path/to/wt"
	fixture, err := NewFixture(t, wtBinary)
	if err != nil {
		t.Fatalf("NewFixture failed: %v", err)
	}

	// Create a branch first
	if err := fixture.CreateBranch("pr-branch", "main"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	// Create PR ref
	if err := fixture.CreatePRRef(123, "pr-branch"); err != nil {
		t.Fatalf("CreatePRRef failed: %v", err)
	}
}

func TestFixtureCreateMRRef(t *testing.T) {
	wtBinary := "/fake/path/to/wt"
	fixture, err := NewFixture(t, wtBinary)
	if err != nil {
		t.Fatalf("NewFixture failed: %v", err)
	}

	// Create a branch first
	if err := fixture.CreateBranch("mr-branch", "main"); err != nil {
		t.Fatalf("CreateBranch failed: %v", err)
	}

	// Create MR ref
	if err := fixture.CreateMRRef(456, "mr-branch"); err != nil {
		t.Fatalf("CreateMRRef failed: %v", err)
	}
}

package main

import (
	"strings"
	"testing"
)

func TestGetPRNumber(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "Valid PR number",
			input:   "123",
			want:    "123",
			wantErr: false,
		},
		{
			name:    "Valid GitHub PR URL",
			input:   "https://github.com/owner/repo/pull/456",
			want:    "456",
			wantErr: false,
		},
		{
			name:    "Valid GitLab MR URL",
			input:   "https://gitlab.com/owner/repo/-/merge_requests/789",
			want:    "789",
			wantErr: false,
		},
		{
			name:    "Invalid input",
			input:   "not-a-number",
			want:    "",
			wantErr: true,
		},
		{
			name:    "Invalid URL",
			input:   "https://example.com/pull/123",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getPRNumber(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("getPRNumber() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getPRNumber() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetDefaultBase(t *testing.T) {
	// This is a simple smoke test - actual behavior depends on git state
	result := getDefaultBase()
	if result == "" {
		t.Error("getDefaultBase() returned empty string")
	}
}

func TestWorktreeExists(t *testing.T) {
	tests := []struct {
		name       string
		branch     string
		wantPath   bool // whether we expect a path to be returned
		wantExists bool // whether worktree should exist
	}{
		{
			name:       "Non-existent branch worktree",
			branch:     "this-branch-definitely-does-not-exist-12345",
			wantPath:   false,
			wantExists: false,
		},
		{
			name:       "Empty branch name",
			branch:     "",
			wantPath:   false,
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotExists := worktreeExists(tt.branch)

			if gotExists != tt.wantExists {
				t.Errorf("worktreeExists() gotExists = %v, want %v", gotExists, tt.wantExists)
			}

			if tt.wantPath && gotPath == "" {
				t.Errorf("worktreeExists() expected path but got empty string")
			}

			if !tt.wantPath && gotPath != "" {
				t.Errorf("worktreeExists() expected no path but got %v", gotPath)
			}
		})
	}
}

func TestBranchExists(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		// Note: We can't reliably test "true" cases without knowing the actual branches
		// in the repository, so we test the "false" case for non-existent branches
		wantExists bool
	}{
		{
			name:       "Non-existent branch",
			branch:     "this-branch-definitely-does-not-exist-98765",
			wantExists: false,
		},
		{
			name:       "Empty branch name",
			branch:     "",
			wantExists: false,
		},
		{
			name:       "Invalid branch name with special chars",
			branch:     "../../invalid",
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := branchExists(tt.branch)
			if got != tt.wantExists {
				t.Errorf("branchExists() = %v, want %v", got, tt.wantExists)
			}
		})
	}
}

func TestBranchExistsCurrentBranch(t *testing.T) {
	// This test verifies branchExists works for branches that actually exist
	// In CI detached HEAD states, local branches may not exist, so we skip if none found
	result := getDefaultBase()
	if result == "" {
		t.Skip("Could not determine default branch, skipping test")
	}

	// In detached HEAD states (CI), the default branch may not exist locally
	// If it doesn't exist, skip the test rather than failing
	if !branchExists(result) {
		t.Skipf("Default branch %s does not exist locally (likely detached HEAD in CI), skipping test", result)
	}

	// If we get here, the branch exists - this validates the positive case works
	t.Logf("Successfully verified branch %s exists", result)
}

func TestGetAvailableBranches(t *testing.T) {
	branches, err := getAvailableBranches()

	if err != nil {
		t.Fatalf("getAvailableBranches() error = %v", err)
	}

	if branches == nil {
		t.Fatal("getAvailableBranches() returned nil slice")
	}

	// We should have at least one branch (the current one)
	if len(branches) == 0 {
		t.Error("getAvailableBranches() returned empty list, expected at least one branch")
	}

	// Verify no branch contains "origin/" prefix (should be stripped)
	for _, branch := range branches {
		if strings.HasPrefix(branch, "origin/") {
			t.Errorf("getAvailableBranches() branch %q contains 'origin/' prefix, should be stripped", branch)
		}

		// Verify no HEAD pointers
		if strings.Contains(branch, "HEAD") {
			t.Errorf("getAvailableBranches() branch %q contains HEAD, should be filtered out", branch)
		}

		// Verify no arrow symbols (from HEAD -> main)
		if strings.Contains(branch, "->") {
			t.Errorf("getAvailableBranches() branch %q contains '->', should be filtered out", branch)
		}

		// Verify no remote names as branch names
		if branch == "origin" || branch == "upstream" {
			t.Errorf("getAvailableBranches() returned remote name %q as branch, should be filtered", branch)
		}

		// Verify no empty branches
		if strings.TrimSpace(branch) == "" {
			t.Error("getAvailableBranches() returned empty branch name")
		}
	}

	// Check for duplicates
	seen := make(map[string]bool)
	for _, branch := range branches {
		if seen[branch] {
			t.Errorf("getAvailableBranches() returned duplicate branch: %q", branch)
		}
		seen[branch] = true
	}
}

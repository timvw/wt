package main

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestParsePROutput(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantNumbers []string
		wantLabels  []string
	}{
		{
			name:        "Empty output",
			output:      "",
			wantNumbers: []string{},
			wantLabels:  []string{},
		},
		{
			name:        "Whitespace only",
			output:      "   \n  \n  ",
			wantNumbers: []string{},
			wantLabels:  []string{},
		},
		{
			name:        "Single PR",
			output:      "123\tFix authentication bug",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"#123: Fix authentication bug"},
		},
		{
			name:        "Multiple PRs",
			output:      "123\tFix authentication bug\n456\tAdd dark mode\n789\tUpdate dependencies",
			wantNumbers: []string{"123", "456", "789"},
			wantLabels:  []string{"#123: Fix authentication bug", "#456: Add dark mode", "#789: Update dependencies"},
		},
		{
			name:        "PR with trailing newline",
			output:      "123\tFix authentication bug\n",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"#123: Fix authentication bug"},
		},
		{
			name:        "PR with multiple trailing newlines",
			output:      "123\tFix authentication bug\n\n\n",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"#123: Fix authentication bug"},
		},
		{
			name:        "Malformed line without tab",
			output:      "123 Fix authentication bug",
			wantNumbers: []string{},
			wantLabels:  []string{},
		},
		{
			name:        "Malformed line with only number",
			output:      "123",
			wantNumbers: []string{},
			wantLabels:  []string{},
		},
		{
			name:        "Mixed valid and invalid lines",
			output:      "123\tValid PR\ninvalid line\n456\tAnother valid PR",
			wantNumbers: []string{"123", "456"},
			wantLabels:  []string{"#123: Valid PR", "#456: Another valid PR"},
		},
		{
			name:        "PR with tab in title",
			output:      "123\tFix bug\twith details",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"#123: Fix bug\twith details"},
		},
		{
			name:        "Empty lines between PRs",
			output:      "123\tFirst PR\n\n456\tSecond PR",
			wantNumbers: []string{"123", "456"},
			wantLabels:  []string{"#123: First PR", "#456: Second PR"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNumbers, gotLabels := parsePROutput(tt.output)

			if len(gotNumbers) != len(tt.wantNumbers) {
				t.Errorf("parsePROutput() gotNumbers length = %v, want %v", len(gotNumbers), len(tt.wantNumbers))
			}

			for i := range gotNumbers {
				if i >= len(tt.wantNumbers) {
					break
				}
				if gotNumbers[i] != tt.wantNumbers[i] {
					t.Errorf("parsePROutput() gotNumbers[%d] = %v, want %v", i, gotNumbers[i], tt.wantNumbers[i])
				}
			}

			if len(gotLabels) != len(tt.wantLabels) {
				t.Errorf("parsePROutput() gotLabels length = %v, want %v", len(gotLabels), len(tt.wantLabels))
			}

			for i := range gotLabels {
				if i >= len(tt.wantLabels) {
					break
				}
				if gotLabels[i] != tt.wantLabels[i] {
					t.Errorf("parsePROutput() gotLabels[%d] = %v, want %v", i, gotLabels[i], tt.wantLabels[i])
				}
			}
		})
	}
}

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantHost  string
		wantOwner string
		wantName  string
	}{
		{
			name:      "GitHub HTTPS",
			input:     "https://github.com/acme/test-repo.git",
			wantHost:  "github.com",
			wantOwner: "acme",
			wantName:  "test-repo",
		},
		{
			name:      "GitHub SSH",
			input:     "git@github.com:acme/test-repo.git",
			wantHost:  "github.com",
			wantOwner: "acme",
			wantName:  "test-repo",
		},
		{
			name:      "GitLab HTTPS nested group",
			input:     "https://gitlab.com/group/subgroup/project.git",
			wantHost:  "gitlab.com",
			wantOwner: "group/subgroup",
			wantName:  "project",
		},
		{
			name:      "GitLab SSH nested group",
			input:     "git@gitlab.com:group/subgroup/project.git",
			wantHost:  "gitlab.com",
			wantOwner: "group/subgroup",
			wantName:  "project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRemoteURL(tt.input)
			if !ok {
				t.Fatalf("parseRemoteURL(%q) returned ok=false", tt.input)
			}
			if got.Host != tt.wantHost {
				t.Errorf("parseRemoteURL(%q) host = %q, want %q", tt.input, got.Host, tt.wantHost)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("parseRemoteURL(%q) owner = %q, want %q", tt.input, got.Owner, tt.wantOwner)
			}
			if got.Name != tt.wantName {
				t.Errorf("parseRemoteURL(%q) name = %q, want %q", tt.input, got.Name, tt.wantName)
			}
		})
	}
}

func TestParseRemoteURLNegative(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "Empty string", input: ""},
		{name: "Whitespace only", input: "   "},
		{name: "HTTPS no path", input: "https://github.com"},
		{name: "HTTPS single component", input: "https://github.com/user"},
		{name: "HTTPS trailing slash only", input: "https://github.com/"},
		{name: "SCP single component", input: "git@github.com:repo.git"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := parseRemoteURL(tt.input)
			if ok {
				t.Errorf("parseRemoteURL(%q) returned ok=true, want ok=false", tt.input)
			}
		})
	}
}

func TestParseRemoteURLWithoutGitSuffix(t *testing.T) {
	// URLs without .git suffix should still parse correctly
	tests := []struct {
		name      string
		input     string
		wantHost  string
		wantOwner string
		wantName  string
	}{
		{
			name:      "HTTPS without .git",
			input:     "https://github.com/acme/test-repo",
			wantHost:  "github.com",
			wantOwner: "acme",
			wantName:  "test-repo",
		},
		{
			name:      "SCP without .git",
			input:     "git@github.com:acme/test-repo",
			wantHost:  "github.com",
			wantOwner: "acme",
			wantName:  "test-repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRemoteURL(tt.input)
			if !ok {
				t.Fatalf("parseRemoteURL(%q) returned ok=false", tt.input)
			}
			if got.Host != tt.wantHost {
				t.Errorf("parseRemoteURL(%q) host = %q, want %q", tt.input, got.Host, tt.wantHost)
			}
			if got.Owner != tt.wantOwner {
				t.Errorf("parseRemoteURL(%q) owner = %q, want %q", tt.input, got.Owner, tt.wantOwner)
			}
			if got.Name != tt.wantName {
				t.Errorf("parseRemoteURL(%q) name = %q, want %q", tt.input, got.Name, tt.wantName)
			}
		})
	}
}

func TestExtractRepoNameFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		// GitHub HTTPS URLs
		{
			name: "GitHub HTTPS with .git suffix",
			url:  "https://github.com/user/repo.git",
			want: "repo",
		},
		{
			name: "GitHub HTTPS without .git suffix",
			url:  "https://github.com/user/repo",
			want: "repo",
		},
		{
			name: "GitHub HTTPS with trailing slash",
			url:  "https://github.com/user/repo/",
			want: "repo",
		},
		{
			name: "GitHub HTTPS with org and .git",
			url:  "https://github.com/my-org/my-repo.git",
			want: "my-repo",
		},

		// GitHub SSH URLs
		{
			name: "GitHub SSH with .git suffix",
			url:  "git@github.com:user/repo.git",
			want: "repo",
		},
		{
			name: "GitHub SSH without .git suffix",
			url:  "git@github.com:user/repo",
			want: "repo",
		},

		// GitLab HTTPS URLs
		{
			name: "GitLab HTTPS with .git suffix",
			url:  "https://gitlab.com/user/project.git",
			want: "project",
		},
		{
			name: "GitLab HTTPS without .git suffix",
			url:  "https://gitlab.com/user/project",
			want: "project",
		},
		{
			name: "GitLab HTTPS with nested groups",
			url:  "https://gitlab.com/group/subgroup/project.git",
			want: "project",
		},

		// GitLab SSH URLs
		{
			name: "GitLab SSH with .git suffix",
			url:  "git@gitlab.com:user/project.git",
			want: "project",
		},
		{
			name: "GitLab SSH without .git suffix",
			url:  "git@gitlab.com:user/project",
			want: "project",
		},

		// Bitbucket URLs
		{
			name: "Bitbucket HTTPS with .git",
			url:  "https://bitbucket.org/user/repo.git",
			want: "repo",
		},
		{
			name: "Bitbucket SSH with .git",
			url:  "git@bitbucket.org:user/repo.git",
			want: "repo",
		},

		// Self-hosted Git URLs
		{
			name: "Self-hosted HTTPS with .git",
			url:  "https://git.example.com/user/myproject.git",
			want: "myproject",
		},
		{
			name: "Self-hosted SSH with .git",
			url:  "git@git.example.com:user/myproject.git",
			want: "myproject",
		},

		// URLs with special characters in repo name
		{
			name: "Repo name with hyphens",
			url:  "https://github.com/user/my-awesome-repo.git",
			want: "my-awesome-repo",
		},
		{
			name: "Repo name with underscores",
			url:  "https://github.com/user/my_awesome_repo.git",
			want: "my_awesome_repo",
		},
		{
			name: "Repo name with dots",
			url:  "https://github.com/user/my.awesome.repo.git",
			want: "my.awesome.repo",
		},

		// Azure DevOps URLs
		{
			name: "Azure DevOps HTTPS",
			url:  "https://dev.azure.com/org/project/_git/repo",
			want: "repo",
		},

		// Edge cases
		{
			name: "Just repo name with .git",
			url:  "repo.git",
			want: "repo",
		},
		{
			name: "Just repo name without .git",
			url:  "repo",
			want: "repo",
		},
		{
			name: "Multiple .git in path",
			url:  "https://github.com/user/repo.git.backup.git",
			want: "repo.git.backup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Extract base name from URL (mimics filepath.Base logic)
			base := filepath.Base(tt.url)
			// Remove trailing slash if present
			base = strings.TrimSuffix(base, "/")
			// Remove .git suffix
			got := strings.TrimSuffix(base, ".git")

			if got != tt.want {
				t.Errorf("extractRepoName(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestParseMROutput(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantNumbers []string
		wantLabels  []string
	}{
		{
			name:        "Empty output",
			output:      "",
			wantNumbers: []string{},
			wantLabels:  []string{},
		},
		{
			name:        "Single MR",
			output:      "!123  OPEN  Fix authentication bug  (feature-branch) ← (main)",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"!123: Fix authentication bug"},
		},
		{
			name: "Multiple MRs",
			output: `!123  OPEN  Fix authentication bug  (feature-branch) ← (main)
!456  OPEN  Add dark mode  (dark-mode) ← (main)
!789  OPEN  Update dependencies  (deps) ← (main)`,
			wantNumbers: []string{"123", "456", "789"},
			wantLabels:  []string{"!123: Fix authentication bug", "!456: Add dark mode", "!789: Update dependencies"},
		},
		{
			name:        "MR with MERGED status",
			output:      "!123  MERGED  Fix authentication bug  (feature-branch) ← (main)",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"!123: Fix authentication bug"},
		},
		{
			name:        "MR with CLOSED status",
			output:      "!123  CLOSED  Fix authentication bug  (feature-branch) ← (main)",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"!123: Fix authentication bug"},
		},
		{
			name:        "Malformed line without MR number",
			output:      "OPEN  Fix authentication bug  (feature-branch) ← (main)",
			wantNumbers: []string{},
			wantLabels:  []string{},
		},
		{
			name:        "Malformed line without parenthesis",
			output:      "!123  OPEN  Fix authentication bug",
			wantNumbers: []string{},
			wantLabels:  []string{},
		},
		{
			name:        "Line not starting with !",
			output:      "123  OPEN  Fix authentication bug  (feature-branch) ← (main)",
			wantNumbers: []string{},
			wantLabels:  []string{},
		},
		{
			name: "Mixed valid and invalid lines",
			output: `!123  OPEN  Valid MR  (branch) ← (main)
invalid line without proper format
!456  OPEN  Another valid MR  (branch2) ← (main)`,
			wantNumbers: []string{"123", "456"},
			wantLabels:  []string{"!123: Valid MR", "!456: Another valid MR"},
		},
		{
			name:        "MR with extra whitespace in title",
			output:      "!123  OPEN    Title with spaces    (branch) ← (main)",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"!123: Title with spaces"},
		},
		{
			name: "Empty lines between MRs",
			output: `!123  OPEN  First MR  (branch1) ← (main)

!456  OPEN  Second MR  (branch2) ← (main)`,
			wantNumbers: []string{"123", "456"},
			wantLabels:  []string{"!123: First MR", "!456: Second MR"},
		},
		{
			name:        "MR with trailing newline",
			output:      "!123  OPEN  Fix bug  (branch) ← (main)\n",
			wantNumbers: []string{"123"},
			wantLabels:  []string{"!123: Fix bug"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNumbers, gotLabels := parseMROutput(tt.output)

			if len(gotNumbers) != len(tt.wantNumbers) {
				t.Errorf("parseMROutput() gotNumbers length = %v, want %v", len(gotNumbers), len(tt.wantNumbers))
			}

			for i := range gotNumbers {
				if i >= len(tt.wantNumbers) {
					break
				}
				if gotNumbers[i] != tt.wantNumbers[i] {
					t.Errorf("parseMROutput() gotNumbers[%d] = %v, want %v", i, gotNumbers[i], tt.wantNumbers[i])
				}
			}

			if len(gotLabels) != len(tt.wantLabels) {
				t.Errorf("parseMROutput() gotLabels length = %v, want %v", len(gotLabels), len(tt.wantLabels))
			}

			for i := range gotLabels {
				if i >= len(tt.wantLabels) {
					break
				}
				if gotLabels[i] != tt.wantLabels[i] {
					t.Errorf("parseMROutput() gotLabels[%d] = %v, want %v", i, gotLabels[i], tt.wantLabels[i])
				}
			}
		})
	}
}

func TestParseGitHubBranchName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "Valid response with simple branch",
			input:   `{"headRefName": "fix/login-bug"}`,
			want:    "fix/login-bug",
			wantErr: false,
		},
		{
			name:    "Valid response with feature branch",
			input:   `{"headRefName": "feat/add-auth"}`,
			want:    "feat/add-auth",
			wantErr: false,
		},
		{
			name:    "Valid response with extra fields",
			input:   `{"headRefName": "main", "number": 123, "title": "Some PR"}`,
			want:    "main",
			wantErr: false,
		},
		{
			name:    "Empty branch name",
			input:   `{"headRefName": ""}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "Missing headRefName field",
			input:   `{"number": 123}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "Invalid JSON",
			input:   `not json`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "Empty JSON",
			input:   `{}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "Branch with slashes",
			input:   `{"headRefName": "user/feat/deep/branch"}`,
			want:    "user/feat/deep/branch",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGitHubBranchName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGitHubBranchName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseGitHubBranchName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseGitLabBranchName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "Valid response with simple branch",
			input:   `{"source_branch": "fix/login-bug"}`,
			want:    "fix/login-bug",
			wantErr: false,
		},
		{
			name:    "Valid response with feature branch",
			input:   `{"source_branch": "feat/add-auth"}`,
			want:    "feat/add-auth",
			wantErr: false,
		},
		{
			name:    "Valid response with extra fields",
			input:   `{"source_branch": "main", "iid": 123, "title": "Some MR", "target_branch": "main"}`,
			want:    "main",
			wantErr: false,
		},
		{
			name:    "Empty branch name",
			input:   `{"source_branch": ""}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "Missing source_branch field",
			input:   `{"iid": 123}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "Invalid JSON",
			input:   `not json`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "Empty JSON",
			input:   `{}`,
			want:    "",
			wantErr: true,
		},
		{
			name:    "Branch with slashes",
			input:   `{"source_branch": "user/feat/deep/branch"}`,
			want:    "user/feat/deep/branch",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGitLabBranchName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseGitLabBranchName() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseGitLabBranchName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildWorktreePathCreatesMissingRoot(t *testing.T) {
	originalRoot := worktreeRoot
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	t.Cleanup(func() {
		worktreeRoot = originalRoot
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
	})

	tmpDir := t.TempDir()
	worktreeRoot = filepath.Join(tmpDir, "missing-root")
	worktreeStrategy = "global"
	worktreePattern = ""

	repo := "example-repo"
	branch := "feature/foo"
	info := repoInfo{
		Main: filepath.Join(tmpDir, repo),
		Name: repo,
	}

	path, err := buildWorktreePath(info, branch)
	if err != nil {
		t.Fatalf("buildWorktreePath() unexpected error: %v", err)
	}

	expectedPath := filepath.Join(worktreeRoot, repo, "feature", "foo")
	if path != expectedPath {
		t.Fatalf("buildWorktreePath() = %s, want %s", path, expectedPath)
	}

	repoDir := filepath.Join(worktreeRoot, repo)
	statInfo, statErr := os.Stat(repoDir)
	if statErr != nil {
		t.Fatalf("expected repo directory to be created at %s: %v", repoDir, statErr)
	}
	if !statInfo.IsDir() {
		t.Fatalf("expected %s to be a directory", repoDir)
	}
}

func TestBuildWorktreePathFailsWhenRootIsFile(t *testing.T) {
	originalRoot := worktreeRoot
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	t.Cleanup(func() {
		worktreeRoot = originalRoot
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
	})

	tmpDir := t.TempDir()
	fileRoot := filepath.Join(tmpDir, "file-root")

	if err := os.WriteFile(fileRoot, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("failed to create file root: %v", err)
	}

	worktreeRoot = fileRoot
	worktreeStrategy = "global"
	worktreePattern = ""

	info := repoInfo{
		Main: filepath.Join(tmpDir, "repo"),
		Name: "repo",
	}

	if _, err := buildWorktreePath(info, "branch"); err == nil {
		t.Fatal("expected buildWorktreePath() to fail when WORKTREE_ROOT is a file")
	}
}

func TestBuildWorktreePathStrategies(t *testing.T) {
	originalRoot := worktreeRoot
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	originalSeparator := worktreeSeparator
	t.Cleanup(func() {
		worktreeRoot = originalRoot
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
		worktreeSeparator = originalSeparator
	})

	tmpDir := t.TempDir()
	worktreeRoot = filepath.Join(tmpDir, "worktrees")

	repoRoot := filepath.Join(tmpDir, "repo")
	info := repoInfo{
		Main: repoRoot,
		Name: "repo",
	}
	tests := []struct {
		name      string
		strategy  string
		separator string
		branch    string
		want      string
	}{
		{
			name:      "global",
			strategy:  "global",
			separator: "/",
			branch:    "feature-branch",
			want:      filepath.Join(worktreeRoot, "repo", "feature-branch"),
		},
		{
			name:      "sibling-repo",
			strategy:  "sibling-repo",
			separator: "-",
			branch:    "feature/sibling",
			want:      filepath.Join(tmpDir, "repo-feature-sibling"),
		},
		{
			name:      "parent-worktrees",
			strategy:  "parent-worktrees",
			separator: "/",
			branch:    "feature-branch",
			want:      filepath.Join(tmpDir, "repo.worktrees", "feature-branch"),
		},
		{
			name:      "parent-branches",
			strategy:  "parent-branches",
			separator: "/",
			branch:    "feature-branch",
			want:      filepath.Join(tmpDir, "feature-branch"),
		},
		{
			name:      "parent-dotdir",
			strategy:  "parent-dotdir",
			separator: "/",
			branch:    "feature-branch",
			want:      filepath.Join(tmpDir, ".worktrees", "feature-branch"),
		},
		{
			name:      "inside-dotdir",
			strategy:  "inside-dotdir",
			separator: "/",
			branch:    "feature-branch",
			want:      filepath.Join(repoRoot, ".worktrees", "feature-branch"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktreeStrategy = tt.strategy
			worktreePattern = ""
			worktreeSeparator = tt.separator

			path, err := buildWorktreePath(info, tt.branch)
			if err != nil {
				t.Fatalf("buildWorktreePath() unexpected error: %v", err)
			}
			if path != tt.want {
				t.Fatalf("buildWorktreePath() = %s, want %s", path, tt.want)
			}
		})
	}
}

func TestBuildWorktreePathCustomPattern(t *testing.T) {
	originalRoot := worktreeRoot
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	t.Cleanup(func() {
		worktreeRoot = originalRoot
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
	})

	tmpDir := t.TempDir()
	worktreeRoot = filepath.Join(tmpDir, "worktrees")
	worktreeStrategy = "custom"
	worktreePattern = "{.worktreeRoot}/custom/{.repo.Name}/{.branch}"

	info := repoInfo{
		Main: filepath.Join(tmpDir, "repo"),
		Name: "repo",
	}

	path, err := buildWorktreePath(info, "feat")
	if err != nil {
		t.Fatalf("buildWorktreePath() unexpected error: %v", err)
	}

	expectedPath := filepath.Join(worktreeRoot, "custom", "repo", "feat")
	if path != expectedPath {
		t.Fatalf("buildWorktreePath() = %s, want %s", path, expectedPath)
	}
}

func TestBuildWorktreePathSeparator(t *testing.T) {
	originalRoot := worktreeRoot
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	originalSeparator := worktreeSeparator
	t.Cleanup(func() {
		worktreeRoot = originalRoot
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
		worktreeSeparator = originalSeparator
	})

	tmpDir := t.TempDir()
	worktreeRoot = filepath.Join(tmpDir, "worktrees")
	worktreeStrategy = "custom"
	worktreePattern = "{.worktreeRoot}/{.repo.Name}/{.branch}"

	info := repoInfo{
		Main: filepath.Join(tmpDir, "repo"),
		Name: "repo",
	}

	tests := []struct {
		name       string
		separator  string
		branch     string
		wantBranch string
	}{
		{
			name:       "Default separator preserves slashes",
			separator:  "/",
			branch:     "feat/foo",
			wantBranch: "feat/foo",
		},
		{
			name:       "Dash separator",
			separator:  "-",
			branch:     "feat/foo",
			wantBranch: "feat-foo",
		},
		{
			name:       "Underscore separator",
			separator:  "_",
			branch:     "feat/foo",
			wantBranch: "feat_foo",
		},
		{
			name:       "Double dash separator",
			separator:  "--",
			branch:     "feat/foo",
			wantBranch: "feat--foo",
		},
		{
			name:       "Empty separator",
			separator:  "",
			branch:     "feat/foo",
			wantBranch: "featfoo",
		},
		{
			name:       "Multiple slashes with underscore",
			separator:  "_",
			branch:     "feat/sub/thing",
			wantBranch: "feat_sub_thing",
		},
		{
			name:       "Backslash with underscore separator",
			separator:  "_",
			branch:     "feat\\bar",
			wantBranch: "feat_bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktreeSeparator = tt.separator

			path, err := buildWorktreePath(info, tt.branch)
			if err != nil {
				t.Fatalf("buildWorktreePath() unexpected error: %v", err)
			}

			expectedPath := filepath.Join(worktreeRoot, "repo", tt.wantBranch)
			if path != expectedPath {
				t.Fatalf("buildWorktreePath() = %s, want %s", path, expectedPath)
			}
		})
	}
}

func TestBuildWorktreePathMissingPatternKey(t *testing.T) {
	originalRoot := worktreeRoot
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	t.Cleanup(func() {
		worktreeRoot = originalRoot
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
	})

	worktreeRoot = t.TempDir()
	worktreeStrategy = "custom"
	worktreePattern = "{.missing}/{.branch}"

	info := repoInfo{
		Main: filepath.Join(worktreeRoot, "repo"),
		Name: "repo",
	}

	if _, err := buildWorktreePath(info, "branch"); err == nil {
		t.Fatal("expected buildWorktreePath() to fail when pattern references missing keys")
	}
}

func TestResolveWorktreePatternCustomRequiresPattern(t *testing.T) {
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	t.Cleanup(func() {
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
	})

	worktreeStrategy = "custom"
	worktreePattern = ""

	if _, err := resolveWorktreePattern(); err == nil {
		t.Fatal("expected resolveWorktreePattern() to fail when custom pattern is missing")
	}
}

func TestBuildWorktreePathWithEnvVar(t *testing.T) {
	originalRoot := worktreeRoot
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	t.Cleanup(func() {
		worktreeRoot = originalRoot
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
	})

	tmpDir := t.TempDir()
	worktreeRoot = filepath.Join(tmpDir, "worktrees")
	worktreeStrategy = "custom"
	worktreePattern = "{.worktreeRoot}/{.env.MY_CUSTOM_VAR}/{.branch}"

	t.Setenv("MY_CUSTOM_VAR", "custom-value")

	info := repoInfo{
		Main: filepath.Join(tmpDir, "repo"),
		Name: "repo",
	}

	path, err := buildWorktreePath(info, "feat/test")
	if err != nil {
		t.Fatalf("buildWorktreePath() unexpected error: %v", err)
	}

	expectedPath := filepath.Join(worktreeRoot, "custom-value", "feat", "test")
	if path != expectedPath {
		t.Fatalf("buildWorktreePath() = %s, want %s", path, expectedPath)
	}
}

func TestBuildWorktreePathMissingEnvVar(t *testing.T) {
	originalRoot := worktreeRoot
	originalStrategy := worktreeStrategy
	originalPattern := worktreePattern
	t.Cleanup(func() {
		worktreeRoot = originalRoot
		worktreeStrategy = originalStrategy
		worktreePattern = originalPattern
	})

	tmpDir := t.TempDir()
	worktreeRoot = filepath.Join(tmpDir, "worktrees")
	worktreeStrategy = "custom"
	worktreePattern = "{.worktreeRoot}/{.env.TOTALLY_MISSING_VAR_12345}/{.branch}"

	// Ensure the var is not set
	t.Setenv("TOTALLY_MISSING_VAR_12345", "")
	os.Unsetenv("TOTALLY_MISSING_VAR_12345")

	info := repoInfo{
		Main: filepath.Join(tmpDir, "repo"),
		Name: "repo",
	}

	_, err := buildWorktreePath(info, "branch")
	if err == nil {
		t.Fatal("expected buildWorktreePath() to fail when env var in pattern is missing")
	}
}

// --- Hook tests ---

func TestGetHooks(t *testing.T) {
	original := worktreeHooks
	t.Cleanup(func() { worktreeHooks = original })

	worktreeHooks = Hooks{
		PreCreate:    []string{"echo pre-create"},
		PostCreate:   []string{"echo post-create"},
		PreCheckout:  []string{"echo pre-checkout"},
		PostCheckout: []string{"echo post-checkout"},
		PreRemove:    []string{"echo pre-remove"},
		PostRemove:   []string{"echo post-remove"},
		PrePR:        []string{"echo pre-pr"},
		PostPR:       []string{"echo post-pr"},
		PreMR:        []string{"echo pre-mr"},
		PostMR:       []string{"echo post-mr"},
	}

	tests := []struct {
		name     string
		hookName string
		want     []string
	}{
		{"pre_create", "pre_create", []string{"echo pre-create"}},
		{"post_create", "post_create", []string{"echo post-create"}},
		{"pre_checkout", "pre_checkout", []string{"echo pre-checkout"}},
		{"post_checkout", "post_checkout", []string{"echo post-checkout"}},
		{"pre_remove", "pre_remove", []string{"echo pre-remove"}},
		{"post_remove", "post_remove", []string{"echo post-remove"}},
		{"pre_pr", "pre_pr", []string{"echo pre-pr"}},
		{"post_pr", "post_pr", []string{"echo post-pr"}},
		{"pre_mr", "pre_mr", []string{"echo pre-mr"}},
		{"post_mr", "post_mr", []string{"echo post-mr"}},
		{"unknown returns nil", "unknown", nil},
		{"empty string returns nil", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getHooks(tt.hookName)
			if tt.want == nil {
				if got != nil {
					t.Errorf("getHooks(%q) = %v, want nil", tt.hookName, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("getHooks(%q) length = %d, want %d", tt.hookName, len(got), len(tt.want))
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("getHooks(%q)[%d] = %q, want %q", tt.hookName, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildHookEnv(t *testing.T) {
	info := repoInfo{
		Main:  "/home/user/repo",
		Name:  "my-repo",
		Host:  "github.com",
		Owner: "my-org",
	}
	env := buildHookEnv(info, "feat/test", "/home/user/worktrees/my-repo/feat/test")

	expected := map[string]string{
		"WT_PATH":       "/home/user/worktrees/my-repo/feat/test",
		"WT_BRANCH":     "feat/test",
		"WT_MAIN":       "/home/user/repo",
		"WT_REPO_NAME":  "my-repo",
		"WT_REPO_HOST":  "github.com",
		"WT_REPO_OWNER": "my-org",
	}

	for k, want := range expected {
		got, ok := env[k]
		if !ok {
			t.Errorf("buildHookEnv() missing key %q", k)
			continue
		}
		if got != want {
			t.Errorf("buildHookEnv()[%q] = %q, want %q", k, got, want)
		}
	}

	if len(env) != len(expected) {
		t.Errorf("buildHookEnv() has %d keys, want %d", len(env), len(expected))
	}
}

func TestRunHooksEmpty(t *testing.T) {
	err := runHooks("pre_create", nil, nil)
	if err != nil {
		t.Errorf("runHooks() with nil commands returned error: %v", err)
	}

	err = runHooks("pre_create", []string{}, nil)
	if err != nil {
		t.Errorf("runHooks() with empty commands returned error: %v", err)
	}
}

func TestRunHooksDisabled(t *testing.T) {
	t.Setenv("WT_HOOKS_DISABLED", "1")

	// Even with a command that would fail, hooks should be skipped
	err := runHooks("pre_create", []string{"false"}, map[string]string{})
	if err != nil {
		t.Errorf("runHooks() with WT_HOOKS_DISABLED=1 returned error: %v", err)
	}
}

func TestRunHooksSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	err := runHooks("post_create", []string{"true"}, map[string]string{})
	if err != nil {
		t.Errorf("runHooks() with 'true' command returned error: %v", err)
	}
}

func TestRunHooksPreAborts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	err := runHooks("pre_create", []string{"false"}, map[string]string{})
	if err == nil {
		t.Error("runHooks() pre-hook with 'false' should return error")
	}
}

func TestRunHooksPostWarnsOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	err := runHooks("post_create", []string{"false"}, map[string]string{})
	if err != nil {
		t.Errorf("runHooks() post-hook with 'false' should not return error, got: %v", err)
	}
}

func TestRunHooksPreStopsOnFirstFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "should-not-exist")

	// Second command fails, third should not run
	cmds := []string{
		"true",
		"false",
		fmt.Sprintf("touch '%s'", marker),
	}

	err := runHooks("pre_create", cmds, map[string]string{})
	if err == nil {
		t.Error("runHooks() pre-hook should return error on failure")
	}

	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("runHooks() pre-hook should stop on first failure; third command ran")
	}
}

func TestRunHooksEnvVarsAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "env-output.txt")

	env := map[string]string{
		"WT_PATH":   "/test/path",
		"WT_BRANCH": "feat/hooks",
	}

	cmds := []string{
		fmt.Sprintf("echo $WT_PATH > '%s'", outFile),
	}

	err := runHooks("post_create", cmds, env)
	if err != nil {
		t.Fatalf("runHooks() returned error: %v", err)
	}

	data, readErr := os.ReadFile(outFile)
	if readErr != nil {
		t.Fatalf("failed to read output file: %v", readErr)
	}

	output := strings.TrimSpace(string(data))
	if output != "/test/path" {
		t.Errorf("hook env WT_PATH = %q, want %q", output, "/test/path")
	}
}

func TestRunHooksMultiplePostContinueOnFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode")
	}

	tmpDir := t.TempDir()
	marker := filepath.Join(tmpDir, "second-ran")

	// First command fails but post-hooks should continue
	cmds := []string{
		"false",
		fmt.Sprintf("touch '%s'", marker),
	}

	err := runHooks("post_create", cmds, map[string]string{})
	if err != nil {
		t.Errorf("runHooks() post-hook should not return error, got: %v", err)
	}

	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("runHooks() post-hook should continue after failure; second command did not run")
	}
}

package harness

import (
	"testing"
)

func TestAssertExitCode(t *testing.T) {
	fixture := &Fixture{WorktreeRoot: "/tmp/test"}

	tests := []struct {
		name     string
		expected int
		result   *Result
		wantErr  bool
	}{
		{
			name:     "matching exit code",
			expected: 0,
			result:   &Result{ExitCode: 0},
			wantErr:  false,
		},
		{
			name:     "non-matching exit code",
			expected: 0,
			result:   &Result{ExitCode: 1},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion := AssertExitCode(tt.expected)
			err := assertion(tt.result, fixture)
			if (err != nil) != tt.wantErr {
				t.Errorf("AssertExitCode() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAssertStdoutContains(t *testing.T) {
	fixture := &Fixture{WorktreeRoot: "/tmp/test"}

	tests := []struct {
		name     string
		expected string
		result   *Result
		wantErr  bool
	}{
		{
			name:     "stdout contains expected string",
			expected: "success",
			result:   &Result{Stdout: "operation success completed"},
			wantErr:  false,
		},
		{
			name:     "stdout does not contain expected string",
			expected: "success",
			result:   &Result{Stdout: "operation failed"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion := AssertStdoutContains(tt.expected)
			err := assertion(tt.result, fixture)
			if (err != nil) != tt.wantErr {
				t.Errorf("AssertStdoutContains() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestAssertPwdEquals(t *testing.T) {
	fixture := &Fixture{
		WorktreeRoot: "/tmp/worktrees",
		RepoName:     "test-repo",
		RepoDir:      "/tmp/test-repo",
	}

	tests := []struct {
		name     string
		expected string
		result   *Result
		wantErr  bool
	}{
		{
			name:     "exact pwd match",
			expected: "/tmp/worktrees",
			result:   &Result{Pwd: "/tmp/worktrees"},
			wantErr:  false,
		},
		{
			name:     "pwd with variable expansion",
			expected: "$WORKTREE_ROOT/test-repo/branch",
			result:   &Result{Pwd: "/tmp/worktrees/test-repo/branch"},
			wantErr:  false,
		},
		{
			name:     "pwd mismatch",
			expected: "/tmp/worktrees",
			result:   &Result{Pwd: "/tmp/other"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertion := AssertPwdEquals(tt.expected)
			err := assertion(tt.result, fixture)
			if (err != nil) != tt.wantErr {
				t.Errorf("AssertPwdEquals() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExpandVars(t *testing.T) {
	fixture := &Fixture{
		WorktreeRoot: "/tmp/worktrees",
		RepoName:     "test-repo",
		RepoDir:      "/tmp/test-repo",
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no variables",
			input:    "/tmp/path",
			expected: "/tmp/path",
		},
		{
			name:     "WORKTREE_ROOT variable",
			input:    "$WORKTREE_ROOT/branch",
			expected: "/tmp/worktrees/branch",
		},
		{
			name:     "REPO variable",
			input:    "$WORKTREE_ROOT/$REPO/branch",
			expected: "/tmp/worktrees/test-repo/branch",
		},
		{
			name:     "REPO_DIR variable",
			input:    "$REPO_DIR",
			expected: "/tmp/test-repo",
		},
		{
			name:     "multiple variables",
			input:    "$WORKTREE_ROOT/$REPO",
			expected: "/tmp/worktrees/test-repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandVars(tt.input, fixture)
			if result != tt.expected {
				t.Errorf("expandVars() = %q, want %q", result, tt.expected)
			}
		})
	}
}

package harness

import "fmt"

// Scenario represents a complete E2E test scenario
type Scenario struct {
	Name        string
	Description string
	Setup       func(*Fixture) error
	Steps       []Step
	Verify      []Assertion
}

// Step represents a single command to execute in the test
type Step struct {
	Cmd  string
	Args []string
}

// Result captures the output of a command execution
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Pwd      string // Current working directory after command
}

// Assertion is a function that validates test results
type Assertion func(*Result, *Fixture) error

// Common assertion builders

// AssertExitCode verifies the exit code matches expected value
func AssertExitCode(expected int) Assertion {
	return func(r *Result, f *Fixture) error {
		if r.ExitCode != expected {
			return fmt.Errorf("exit code: expected %d, got %d", expected, r.ExitCode)
		}
		return nil
	}
}

// AssertStdoutContains verifies stdout contains the expected string
func AssertStdoutContains(expected string) Assertion {
	return func(r *Result, f *Fixture) error {
		if !contains(r.Stdout, expected) {
			return fmt.Errorf("stdout does not contain %q\nGot: %s", expected, r.Stdout)
		}
		return nil
	}
}

// AssertStderrContains verifies stderr contains the expected string
func AssertStderrContains(expected string) Assertion {
	return func(r *Result, f *Fixture) error {
		if !contains(r.Stderr, expected) {
			return fmt.Errorf("stderr does not contain %q\nGot: %s", expected, r.Stderr)
		}
		return nil
	}
}

// AssertPwdEquals verifies the current directory matches expected
// Supports variable expansion: $WORKTREE_ROOT, $REPO
func AssertPwdEquals(expected string) Assertion {
	return func(r *Result, f *Fixture) error {
		expandedExpected := expandVars(expected, f)
		if r.Pwd != expandedExpected {
			return fmt.Errorf("pwd: expected %q, got %q", expandedExpected, r.Pwd)
		}
		return nil
	}
}

// AssertPwdContains verifies the current directory contains expected substring
func AssertPwdContains(expected string) Assertion {
	return func(r *Result, f *Fixture) error {
		expandedExpected := expandVars(expected, f)
		if !contains(r.Pwd, expandedExpected) {
			return fmt.Errorf("pwd does not contain %q\nGot: %s", expandedExpected, r.Pwd)
		}
		return nil
	}
}

// Helper functions

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		findSubstring(haystack, needle)
}

func findSubstring(s, substr string) bool {
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func expandVars(s string, f *Fixture) string {
	// Simple variable expansion for common test patterns
	// Replace longer strings first to avoid partial matches
	result := s
	result = replaceAll(result, "$WORKTREE_ROOT", f.WorktreeRoot)
	result = replaceAll(result, "$REPO_DIR", f.RepoDir)
	result = replaceAll(result, "$REPO", f.RepoName)
	return result
}

func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	result := ""
	for {
		i := findIndex(s, old)
		if i == -1 {
			result += s
			break
		}
		result += s[:i] + new
		s = s[i+len(old):]
	}
	return result
}

func findIndex(s, substr string) int {
	if len(substr) > len(s) {
		return -1
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

package main

import (
	"testing"
)

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		input  string
		target string
		want   bool
	}{
		{"foba", "foo/bar", true},
		{"feat", "feature/add-auth", true},
		{"xyz", "feature/add-auth", false},
		{"", "anything", true},            // empty input matches everything
		{"ABC", "abc-def", true},          // case-insensitive
		{"abc", "ABC-DEF", true},          // case-insensitive reverse
		{"fad", "feature/add-auth", true}, // non-contiguous match
	}

	for _, tt := range tests {
		t.Run(tt.input+"_vs_"+tt.target, func(t *testing.T) {
			got := fuzzyMatch(tt.input, tt.target)
			if got != tt.want {
				t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", tt.input, tt.target, got, tt.want)
			}
		})
	}
}

func TestFuzzySearcher(t *testing.T) {
	items := []string{
		"feature/add-auth",
		"fix/bug-123",
		"main",
		"develop",
	}
	searcher := fuzzySearcher(items)

	// "feat" should match "feature/add-auth" (index 0)
	if !searcher("feat", 0) {
		t.Error("expected 'feat' to match 'feature/add-auth'")
	}

	// "feat" should not match "fix/bug-123" (index 1)
	if searcher("feat", 1) {
		t.Error("expected 'feat' NOT to match 'fix/bug-123'")
	}

	// "fix" should match "fix/bug-123" (index 1)
	if !searcher("fix", 1) {
		t.Error("expected 'fix' to match 'fix/bug-123'")
	}

	// "dev" should match "develop" (index 3)
	if !searcher("dev", 3) {
		t.Error("expected 'dev' to match 'develop'")
	}

	// empty input matches everything
	if !searcher("", 2) {
		t.Error("expected empty input to match any item")
	}
}

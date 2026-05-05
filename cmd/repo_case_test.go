package cmd

import "testing"

func TestFindWorktreeByBranchCaseInsensitiveFallback(t *testing.T) {
	entries := []worktreeListEntry{
		{Path: "/worktrees/repo/Feature/make-it-work", Branch: "Feature/make-it-work"},
	}

	if got, ok := findWorktreeByBranch(entries, "feature/make-it-work", false); ok || got != "" {
		t.Fatalf("case-sensitive lookup = (%q, %v), want no match", got, ok)
	}

	got, ok := findWorktreeByBranch(entries, "feature/make-it-work", true)
	if !ok {
		t.Fatal("case-insensitive lookup did not find worktree")
	}
	if want := "/worktrees/repo/Feature/make-it-work"; got != want {
		t.Fatalf("case-insensitive lookup path = %q, want %q", got, want)
	}
}

func TestFindWorktreeByBranchExactMatchWins(t *testing.T) {
	entries := []worktreeListEntry{
		{Path: "/worktrees/repo/Feature/make-it-work", Branch: "Feature/make-it-work"},
		{Path: "/worktrees/repo/feature/make-it-work", Branch: "feature/make-it-work"},
	}

	got, ok := findWorktreeByBranch(entries, "feature/make-it-work", true)
	if !ok {
		t.Fatal("lookup did not find exact worktree")
	}
	if want := "/worktrees/repo/feature/make-it-work"; got != want {
		t.Fatalf("lookup path = %q, want exact path %q", got, want)
	}
}

package cmd

import (
	"strings"
	"testing"
)

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

func TestFindWorktreeByBranchPathWithSpaces(t *testing.T) {
	entries := []worktreeListEntry{
		{Path: "/Users/John Doe/dev/worktrees/repo/main", Branch: "main"},
		{Path: "/Users/John Doe/dev/worktrees/repo/feature-x", Branch: "feature-x"},
	}

	got, ok := findWorktreeByBranch(entries, "feature-x", false)
	if !ok {
		t.Fatal("lookup did not find worktree with spaces in path")
	}
	if want := "/Users/John Doe/dev/worktrees/repo/feature-x"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestParsePorcelainWithSpacesInPath(t *testing.T) {
	// Simulate git worktree list --porcelain output with spaces in paths.
	// The old non-porcelain approach using strings.Fields would split
	// "C:\Users\John Doe\dev\repo" into ["C:\Users\John", "Doe\dev\repo"].
	// The porcelain format handles this correctly.
	porcelainOutput := "worktree /Users/John Doe/dev/repo\nHEAD abc123\nbranch refs/heads/main\n\n" +
		"worktree /Users/John Doe/dev/worktrees/repo/feature-x\nHEAD def456\nbranch refs/heads/feature-x\n\n"

	var entries []worktreeListEntry
	current := worktreeListEntry{}
	for _, rawLine := range strings.Split(porcelainOutput, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if current.Path != "" {
				entries = append(entries, current)
				current = worktreeListEntry{}
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				entries = append(entries, current)
			}
			current = worktreeListEntry{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			branch := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(branch, "refs/heads/")
		}
	}
	if current.Path != "" {
		entries = append(entries, current)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Path != "/Users/John Doe/dev/repo" {
		t.Errorf("entries[0].Path = %q, want path with space preserved", entries[0].Path)
	}
	if entries[1].Path != "/Users/John Doe/dev/worktrees/repo/feature-x" {
		t.Errorf("entries[1].Path = %q, want path with space preserved", entries[1].Path)
	}
	if entries[0].Branch != "main" {
		t.Errorf("entries[0].Branch = %q, want %q", entries[0].Branch, "main")
	}
}

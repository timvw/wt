package cmd

import (
	"path/filepath"
	"testing"
)

func TestBuildSwitchTargets(t *testing.T) {
	entries := []worktreeListEntry{
		{Path: "/repos/wt", Branch: "main"},
		{Path: "/worktrees/wt/feat/login", Branch: "feat/login"},
		{Path: "/worktrees/wt/detached", Detached: true, HEAD: "abc123"},
		{Path: "/repos/wt-bare", Bare: true},
	}

	targets := buildSwitchTargets(entries, "/worktrees/wt/feat/login/cmd")

	if len(targets) != 3 {
		t.Fatalf("expected 3 targets (bare worktree skipped), got %d", len(targets))
	}

	if targets[0].Label != "main" {
		t.Errorf("expected main worktree to be listed as %q, got %q", "main", targets[0].Label)
	}
	if targets[0].Path != "/repos/wt" {
		t.Errorf("unexpected path for main worktree: %q", targets[0].Path)
	}

	if targets[1].Label != "feat/login (current)" {
		t.Errorf("expected the worktree containing cwd to be marked current, got %q", targets[1].Label)
	}

	if targets[2].Label != "detached (detached)" {
		t.Errorf("expected detached worktree to fall back to its directory name, got %q", targets[2].Label)
	}
	if targets[2].Branch != "" {
		t.Errorf("expected detached worktree to have no branch, got %q", targets[2].Branch)
	}
}

func TestBuildSwitchTargetsWithoutCwd(t *testing.T) {
	entries := []worktreeListEntry{{Path: "/repos/wt", Branch: "main"}}

	targets := buildSwitchTargets(entries, "")

	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].Label != "main" {
		t.Errorf("expected no current marker when cwd is unknown, got %q", targets[0].Label)
	}
}

func TestIsSameOrInsidePath(t *testing.T) {
	tests := []struct {
		name   string
		cwd    string
		target string
		want   bool
	}{
		{"same path", "/worktrees/feat", "/worktrees/feat", true},
		{"inside path", "/worktrees/feat/cmd", "/worktrees/feat", true},
		{"outside path", "/worktrees/other", "/worktrees/feat", false},
		{"sibling with shared prefix", "/worktrees/feature", "/worktrees/feat", false},
		{"parent of target", "/worktrees", "/worktrees/feat", false},
		{"empty cwd", "", "/worktrees/feat", false},
		{"empty target", "/worktrees/feat", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSameOrInsidePath(filepath.FromSlash(tt.cwd), filepath.FromSlash(tt.target))
			if got != tt.want {
				t.Errorf("isSameOrInsidePath(%q, %q) = %v, want %v", tt.cwd, tt.target, got, tt.want)
			}
		})
	}
}

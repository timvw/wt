package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneCommandRegistered(t *testing.T) {
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "clone" {
			found = true
			if got := c.Aliases; len(got) != 1 || got[0] != "cl" {
				t.Errorf("clone aliases = %v, want [cl]", got)
			}
		}
	}
	if !found {
		t.Fatal("clone command not registered with root command")
	}
}

func TestLooksLikeGHNameWithOwner(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"timvw/wt", true},
		{"owner/repo", true},
		{"git@github.com:timvw/wt.git", false},
		{"https://github.com/timvw/wt.git", false},
		{"./local/path", false},
		{"../rel", false},
		{"/abs/path", false},
		{"~/home/path", false},
		{"just-a-name", false},
		{"too/many/parts", false},
		{"/leading", false},
		{"owner/", false},
	}
	for _, c := range cases {
		if got := looksLikeGHNameWithOwner(c.in); got != c.want {
			t.Errorf("looksLikeGHNameWithOwner(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRepoPlacementPathNested(t *testing.T) {
	repoPattern = defaultRepoPattern
	cat := Category{RepoRoot: "/tmp/repos/work"}
	info := repoInfo{Host: "github.com", Owner: "timvw", Name: "wt"}

	// Empty URL → resolveDefaultBranch returns "main" (git ls-remote unreachable).
	got, err := repoPlacementPath("work", cat, info, "")
	if err != nil {
		t.Fatalf("repoPlacementPath: %v", err)
	}
	want := filepath.Clean("/tmp/repos/work/timvw/wt/main")
	if got != want {
		t.Errorf("repoPlacementPath = %q, want %q", got, want)
	}
}

func TestRepoPlacementPathWithBranchLiteral(t *testing.T) {
	repoPattern = "{.category.RepoRoot}/{.repo.Owner}/{.repo.Name}/{.branch}"
	defer func() { repoPattern = defaultRepoPattern }()
	cat := Category{RepoRoot: "/tmp/repos/oss"}
	info := repoInfo{Host: "github.com", Owner: "timvw", Name: "wt"}

	// Stub resolveDefaultBranch by pre-setting a known value via a wrapper test.
	// We can't mock gh in unit tests, so verify the path shape when branch = "main"
	// by using the resolveDefaultBranch fallback (no gh in test env → "main").
	got, err := repoPlacementPath("oss", cat, info, "")
	if err != nil {
		t.Fatalf("repoPlacementPath: %v", err)
	}
	// Branch will be whatever resolveDefaultBranch returns (likely "main" in CI).
	// Just verify it's non-empty and the path structure is correct.
	if got == filepath.Clean("/tmp/repos/oss/timvw/wt") {
		t.Errorf("repoPlacementPath with {.branch} should include a branch segment, got %q", got)
	}
	if !strings.HasPrefix(got, "/tmp/repos/oss/timvw/wt/") {
		t.Errorf("repoPlacementPath = %q, want prefix /tmp/repos/oss/timvw/wt/", got)
	}
}

func TestRepoPlacementPathRequiresRepoInfo(t *testing.T) {
	cat := Category{RepoRoot: "/tmp/repos/work"}
	// Missing owner/host (e.g. a local path source) must error, prompting for
	// an explicit destination instead of producing a malformed path.
	if _, err := repoPlacementPath("work", cat, repoInfo{Name: "wt"}, ""); err == nil {
		t.Error("expected error for incomplete repoInfo, got nil")
	}
}

func TestResolveCloneURLPassthrough(t *testing.T) {
	cat := Category{}
	for _, src := range []string{
		"https://github.com/timvw/wt.git",
		"git@github.com:timvw/wt.git",
		"/abs/local/repo",
		"./rel/repo",
	} {
		got, err := resolveCloneURL(src, cat)
		if err != nil {
			t.Errorf("resolveCloneURL(%q) error: %v", src, err)
		}
		if got != src {
			t.Errorf("resolveCloneURL(%q) = %q, want passthrough", src, got)
		}
	}
}

func TestResolveCategoryDerivesRepoRoot(t *testing.T) {
	origRepos := reposRoot
	origCats := configCategories
	origDefaults := categoryDefaults
	t.Cleanup(func() {
		reposRoot = origRepos
		configCategories = origCats
		categoryDefaults = origDefaults
	})

	reposRoot = "/base/repos"
	categoryDefaults = Category{}
	configCategories = map[string]Category{
		"custom": {GHAuth: "x"},          // no repo_root -> derive from base
		"pinned": {RepoRoot: "/explicit"}, // explicit repo_root wins
	}

	// Builtin with no repo_root derives <base>/<name>.
	if work, ok := resolveCategory("work"); !ok || work.RepoRoot != filepath.Join("/base/repos", "work") {
		t.Errorf("work RepoRoot = %q, want /base/repos/work", work.RepoRoot)
	}
	// Config category without repo_root also derives.
	if c, _ := resolveCategory("custom"); c.RepoRoot != filepath.Join("/base/repos", "custom") {
		t.Errorf("custom RepoRoot = %q, want /base/repos/custom", c.RepoRoot)
	}
	// Explicit repo_root is preserved, not derived.
	if p, _ := resolveCategory("pinned"); p.RepoRoot != "/explicit" {
		t.Errorf("pinned RepoRoot = %q, want /explicit", p.RepoRoot)
	}
}

func TestResolveCategoryBuiltins(t *testing.T) {
	configCategories = map[string]Category{}
	categoryDefaults = Category{}
	for _, name := range []string{"work", "personal", "oss"} {
		if _, ok := resolveCategory(name); !ok {
			t.Errorf("builtin category %q not resolvable", name)
		}
	}
	if _, ok := resolveCategory("does-not-exist"); ok {
		t.Error("unknown category resolved unexpectedly")
	}
}

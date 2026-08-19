package cmd

import (
	"path/filepath"
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

// withCloneConfig points the clone placement globals at a fresh temp directory
// for the duration of a test and returns that root. t.TempDir keeps the root
// absolute on every platform, which a hard-coded "/tmp/repos" is not on Windows.
func withCloneConfig(t *testing.T, pattern string) string {
	t.Helper()
	origRoot, origPattern, origSep := reposRoot, repoPattern, worktreeSeparator
	t.Cleanup(func() {
		reposRoot, repoPattern, worktreeSeparator = origRoot, origPattern, origSep
	})
	reposRoot = t.TempDir()
	repoPattern = pattern
	worktreeSeparator = "/"
	return reposRoot
}

func TestRepoPlacementPathDefault(t *testing.T) {
	root := withCloneConfig(t, defaultRepoPattern)
	info := repoInfo{Host: "github.com", Owner: "timvw", Name: "wt"}

	got, err := repoPlacementPath(info, "main")
	if err != nil {
		t.Fatalf("repoPlacementPath: %v", err)
	}
	want := filepath.Join(root, "github.com", "timvw", "wt", "main")
	if got != want {
		t.Errorf("repoPlacementPath = %q, want %q", got, want)
	}
}

// The env-var escape hatch is what replaces a built-in "category" concept:
// users add their own grouping level through repo_pattern.
func TestRepoPlacementPathEnvCategory(t *testing.T) {
	root := withCloneConfig(t, "{.repoRoot}/{.env.WT_TEST_CATEGORY}/{.repo.Owner}/{.repo.Name}/{.branch}")
	info := repoInfo{Host: "github.com", Owner: "acme", Name: "api"}

	t.Setenv("WT_TEST_CATEGORY", "work")
	got, err := repoPlacementPath(info, "main")
	if err != nil {
		t.Fatalf("repoPlacementPath: %v", err)
	}
	want := filepath.Join(root, "work", "acme", "api", "main")
	if got != want {
		t.Errorf("repoPlacementPath = %q, want %q", got, want)
	}

	// An empty value collapses the segment away rather than leaving "//".
	t.Setenv("WT_TEST_CATEGORY", "")
	got, err = repoPlacementPath(info, "main")
	if err != nil {
		t.Fatalf("repoPlacementPath (empty category): %v", err)
	}
	want = filepath.Join(root, "acme", "api", "main")
	if got != want {
		t.Errorf("repoPlacementPath with empty category = %q, want %q", got, want)
	}
}

// A pattern that omits {.repoRoot} must not clone into the caller's cwd.
func TestRepoPlacementPathAnchorsRelativePattern(t *testing.T) {
	root := withCloneConfig(t, "{.repo.Owner}/{.repo.Name}")
	info := repoInfo{Host: "github.com", Owner: "timvw", Name: "wt"}

	got, err := repoPlacementPath(info, "main")
	if err != nil {
		t.Fatalf("repoPlacementPath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("repoPlacementPath returned relative path %q", got)
	}
	want := filepath.Join(root, "timvw", "wt")
	if got != want {
		t.Errorf("repoPlacementPath = %q, want %q", got, want)
	}
}

// A repo_root that filepath.IsAbs rejects — a relative one, or the Windows
// rooted-but-driveless "\data\repos" — must not get prepended a second time
// when the pattern already renders it.
func TestRepoPlacementPathDoesNotDoubleAnchor(t *testing.T) {
	withCloneConfig(t, defaultRepoPattern)
	reposRoot = filepath.FromSlash("relative/repos")
	info := repoInfo{Host: "github.com", Owner: "timvw", Name: "wt"}

	got, err := repoPlacementPath(info, "main")
	if err != nil {
		t.Fatalf("repoPlacementPath: %v", err)
	}
	want := filepath.Join("relative", "repos", "github.com", "timvw", "wt", "main")
	if got != want {
		t.Errorf("repoPlacementPath = %q, want %q", got, want)
	}
}

func TestRepoPlacementPathRequiresRepoInfo(t *testing.T) {
	withCloneConfig(t, defaultRepoPattern)
	// Missing owner (e.g. a local path source) must error, prompting for an
	// explicit destination instead of producing a malformed path.
	if _, err := repoPlacementPath(repoInfo{Name: "wt"}, "main"); err == nil {
		t.Error("expected error for incomplete repoInfo, got nil")
	}
}

// A URL pasted from an untrusted source must not walk the clone out of
// repo_root: https://host/../../../tmp/pwn.git parses to owner "../../../tmp".
func TestRepoPlacementPathRejectsTraversal(t *testing.T) {
	withCloneConfig(t, defaultRepoPattern)

	// Escapes via the path, and via the host — url.Parse accepts ".." as a
	// hostname, and the default pattern renders {.repo.Host}.
	for _, src := range []string{
		"https://github.com/../../../tmp/pwn.git",
		"https://../owner/repo.git",
	} {
		info, ok := parseRemoteURL(src)
		if !ok {
			t.Fatalf("parseRemoteURL(%q) failed; test no longer exercises the traversal path", src)
		}
		if _, err := repoPlacementPath(info, "main"); err == nil {
			t.Errorf("expected error for %q, got nil", src)
		}
	}

	// Belt and braces: even if a caller hands over a traversing host directly,
	// placement refuses rather than rendering it.
	if _, err := repoPlacementPath(repoInfo{Host: "../escape", Owner: "o", Name: "r"}, "main"); err == nil {
		t.Error("expected error for host containing \"..\", got nil")
	}

	// The same guard applies to the branch segment.
	clean := repoInfo{Host: "github.com", Owner: "timvw", Name: "wt"}
	if _, err := repoPlacementPath(clean, "../../escape"); err == nil {
		t.Error("expected error for branch containing \"..\", got nil")
	}

	// A ".." inside a segment is a legitimate host and must still resolve.
	if _, err := repoPlacementPath(repoInfo{Host: "a..b", Owner: "o", Name: "r"}, "main"); err != nil {
		t.Errorf("host %q should be allowed: %v", "a..b", err)
	}
}

func TestHasDotDotSegment(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"timvw", false},
		{"group/subgroup", false},
		{"..hidden", false}, // sub-segment, not a component
		{"a/..b/c", false},
		{"..", true},
		{"../../tmp", true},
		{"group/../etc", true},
		{`group\..\etc`, true}, // Windows separator
	}
	for _, c := range cases {
		if got := hasDotDotSegment(c.in); got != c.want {
			t.Errorf("hasDotDotSegment(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveCloneURLPassthrough(t *testing.T) {
	for _, src := range []string{
		"https://github.com/timvw/wt.git",
		"git@github.com:timvw/wt.git",
		"/abs/local/repo",
		"./rel/repo",
	} {
		got, err := resolveCloneURL(src)
		if err != nil {
			t.Errorf("resolveCloneURL(%q) error: %v", src, err)
		}
		if got != src {
			t.Errorf("resolveCloneURL(%q) = %q, want passthrough", src, got)
		}
	}
}

package cmd

import (
	"os"
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

// TestRepoPlacementPathRejectsExpansion covers the other half of the traversal
// problem: expandHome runs on the rendered path, so a value the URL supplied is
// still expanded after "{.repo.Owner}" has been substituted into it. "$HOME" is
// not a directory of that name, and neither the ".." check nor the repo_root
// anchoring is looking at what it turns into.
func TestRepoPlacementPathRejectsExpansion(t *testing.T) {
	clean := repoInfo{Host: "github.com", Owner: "timvw", Name: "wt"}

	// Deliberately somewhere the wtStateAtPath backstop will not recognise: the
	// two guards catch the real exploit independently, and this one has to fail
	// when the refusal below is removed, not shrug because the other caught it.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), ".config"))

	refused := []struct {
		what string
		info repoInfo
	}{
		{"$ in the owner", repoInfo{Host: "github.com", Owner: "$HOME/.config", Name: "wt"}},
		{"$ in the host", repoInfo{Host: "${HOME}", Owner: "o", Name: "r"}},
		{"$ in the name", repoInfo{Host: "github.com", Owner: "o", Name: "$HOME"}},
		{"% in the owner", repoInfo{Host: "github.com", Owner: "%APPDATA%", Name: "wt"}},
		{"a leading ~ in the owner", repoInfo{Host: "github.com", Owner: "~/.config", Name: "wt"}},
	}
	for _, tt := range refused {
		t.Run(tt.what, func(t *testing.T) {
			// The pattern that makes it reachable: without {.repoRoot} the
			// rendered path is relative, and an expansion to an absolute one
			// then walks past the anchoring too.
			withCloneConfig(t, "{.repo.Owner}/{.repo.Name}")
			if path, err := repoPlacementPath(tt.info, "main"); err == nil {
				t.Fatalf("repoPlacementPath() = %q, want an error: the URL chose where that expands to", path)
			}
		})
	}

	t.Run("the branch too", func(t *testing.T) {
		withCloneConfig(t, defaultRepoPattern)
		if _, err := repoPlacementPath(clean, "$HOME"); err == nil {
			t.Error("expected an error for a default branch containing \"$\", got nil")
		}
	})

	t.Run("a ~ that is not leading is a directory name", func(t *testing.T) {
		// Only a leading ~ expands, so refusing every one of them would refuse
		// paths that are merely oddly named.
		root := withCloneConfig(t, defaultRepoPattern)
		got, err := repoPlacementPath(repoInfo{Host: "github.com", Owner: "a/~/b", Name: "wt"}, "main")
		if err != nil {
			t.Fatalf("repoPlacementPath() error = %v, want a path", err)
		}
		if want := filepath.Join(root, "github.com", "a", "~", "b", "wt", "main"); got != want {
			t.Errorf("repoPlacementPath() = %q, want %q", got, want)
		}
	})
}

// A clone writes a whole repository at once, so a destination on wt's own state
// is the substitution renderWorktreePath refuses — the cloned config.toml and
// trust.toml become the ones wt reads. Refused here whatever route reached it,
// not only via the expansion above.
func TestClonePlacementIsNeverOnWtsOwnState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfgDir := configDir()

	origConfigFilePath, origSources := configFilePath, configSources
	t.Cleanup(func() { configFilePath, configSources = origConfigFilePath, origSources })
	configFilePath = filepath.Join(cfgDir, "config.toml")
	// Deliberately not a phrase that appears anywhere else in the refusal: the
	// description of the config directory already contains "config file", so a
	// label of that would pass whether or not it reached the message.
	configSources.RepoPattern = "the layer under test"

	withCloneConfig(t, "{.repoRoot}/{.repo.Name}")
	reposRoot = cfgDir

	path, err := repoPlacementPath(repoInfo{Host: "github.com", Owner: "evil", Name: "payload"}, "main")
	if err == nil {
		t.Fatalf("repoPlacementPath() = %q, want an error: the clone lands inside wt's config directory", path)
	}
	// repo_pattern is never read from a repository, so the refusal names the
	// layer the user can go and change.
	if !strings.Contains(err.Error(), "the layer under test") {
		t.Errorf("error does not say where the pattern came from:\n%v", err)
	}
}

// TestCloneRefusesAnExplicitDestinationOnWtsOwnState: the pattern refusal offers
// an explicit destination as the way out, and the way out is a different path —
// not permission to name this one. A repository cloned into wt's config
// directory supplies the config file and the approval store, and a committed
// trust.toml would cover every repository on the machine.
func TestCloneRefusesAnExplicitDestinationOnWtsOwnState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	origConfigFilePath := configFilePath
	t.Cleanup(func() { configFilePath = origConfigFilePath })
	configFilePath = filepath.Join(configDir(), "config.toml")

	// A real repository, so that without the refusal the clone succeeds and the
	// two files below land as wt's own — rather than failing for its own reasons
	// and letting the test pass without proving anything.
	payload := filepath.Join(home, "payload")
	if err := os.MkdirAll(payload, 0o755); err != nil {
		t.Fatal(err)
	}
	setupTestRepo(t, payload)
	for _, name := range []string{"config.toml", "trust.toml"} {
		if err := os.WriteFile(filepath.Join(payload, name), []byte("version = 2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGitCommand(t, payload, "add", "-A")
	runGitCommand(t, payload, "commit", "-qm", "payload")

	err := runClone(cloneCmd, []string{payload, configDir()})
	if err == nil {
		t.Fatal("runClone() = nil, want a refusal: the clone lands on wt's config directory")
	}
	if !strings.Contains(err.Error(), "approval store") {
		t.Errorf("error does not say what the clone would become:\n%v", err)
	}
	if _, statErr := os.Stat(configDir()); !os.IsNotExist(statErr) {
		t.Errorf("something was created at %s (stat err = %v)", configDir(), statErr)
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

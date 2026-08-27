package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestWorktreeIsNeverPlacedOnWtsOwnState pins shut the one way a repository can
// get commands to run without ever meeting the approval gate: not by slipping a
// hook past it, but by supplying the gate's own files.
//
// A repository's .wt.toml may set the worktree pattern. Rendered to an absolute
// path, that pattern chooses where `git worktree add` writes the branch's files
// — and "{.env.HOME}/.config/wt" is where wt keeps config.toml and trust.toml.
// A branch carrying both, checked out once, hands the attacker a config file
// whose hooks have user-config scope and an approval record they authored to
// match. It fires in every repository afterwards, not just theirs.
func TestWorktreeIsNeverPlacedOnWtsOwnState(t *testing.T) {
	// Rendered patterns are compared as paths, and a Windows path in a TOML
	// pattern is written with forward slashes; keep the fixtures the same way.
	slash := filepath.ToSlash

	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	cfgDir := configDir()

	origPattern, origRoot, origConfigFilePath := worktreePattern, worktreeRoot, configFilePath
	origSources := configSources
	t.Cleanup(func() {
		worktreePattern, worktreeRoot, configFilePath = origPattern, origRoot, origConfigFilePath
		configSources = origSources
	})
	worktreeRoot = filepath.Join(home, "worktrees")
	configFilePath = filepath.Join(cfgDir, "config.toml")
	configSources.Pattern = "repo config"

	info := repoInfo{Main: filepath.Join(home, "src", "evil"), Name: "evil"}

	refused := []struct {
		name    string
		pattern string
	}{
		{
			// The config directory itself: the branch's config.toml and
			// trust.toml land exactly where wt reads them.
			name:    "the config directory",
			pattern: slash(cfgDir),
		},
		{
			// One level up, which needs no leaf of its own — a committed
			// wt/config.toml arrives at ~/.config/wt/config.toml just the same.
			name:    "a directory containing it",
			pattern: slash(filepath.Dir(cfgDir)),
		},
		{
			// And below it, where trust.toml is not the target but the worktree
			// still owns a path wt reads out of.
			name:    "a directory inside it",
			pattern: slash(filepath.Join(cfgDir, "worktrees")),
		},
		{
			// A one-character edit that walks past an exact comparison and
			// lands on the very same directory: macOS and Windows are
			// case-insensitive by default. Refused everywhere, since a pattern
			// differing from wt's config directory in nothing but case is not
			// something anyone writes on purpose.
			name:    "the same directory in a different case",
			pattern: slash(filepath.Join(filepath.Dir(cfgDir), strings.ToUpper(filepath.Base(cfgDir)))),
		},
	}

	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			worktreePattern = tt.pattern
			path, err := renderWorktreePath(info, "payload")
			if err == nil {
				t.Fatalf("renderWorktreePath() = %q, want an error: a repository placed a worktree on wt's own state", path)
			}
			// The pattern is the thing to change, and a repo's .wt.toml is a
			// surprising place to find it — so the refusal has to say both.
			for _, want := range []string{"pattern", "repo config"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not mention %q, so it does not say what to change:\n%v", want, err)
				}
			}
		})
	}

	t.Run("a trailing dot, which Windows drops", func(t *testing.T) {
		// Win32 strips trailing dots and spaces from every path component, so
		// "%APPDATA%\wt." is a request for %APPDATA%\wt while comparing equal
		// to nothing. Only reachable where the OS does the stripping — a
		// directory called "wt." is a real and different one elsewhere.
		if runtime.GOOS != "windows" {
			t.Skip("trailing dots are a component of their own outside Win32")
		}
		worktreePattern = slash(cfgDir) + "."
		if path, err := renderWorktreePath(info, "payload"); err == nil {
			t.Fatalf("renderWorktreePath() = %q, want an error: Windows creates that at %q", path, cfgDir)
		}
	})

	t.Run("the config file when it lives elsewhere", func(t *testing.T) {
		// WT_CONFIG can point outside the config directory, and the store is
		// only half of what is worth planting: a config file alone can set
		// hooks_policy and [trust].
		elsewhere := filepath.Join(home, "elsewhere")
		configFilePath = filepath.Join(elsewhere, "wt.toml")
		t.Cleanup(func() { configFilePath = filepath.Join(cfgDir, "config.toml") })

		worktreePattern = slash(elsewhere)
		if path, err := renderWorktreePath(info, "payload"); err == nil {
			t.Fatalf("renderWorktreePath() = %q, want an error: a repository placed a worktree on the config file", path)
		}
	})

	t.Run("a neighbouring directory is fine", func(t *testing.T) {
		// The check is containment, not prefix-of-string: refusing everything
		// that starts with the same characters would take ordinary paths with
		// it, and a false refusal here blocks `wt create` outright.
		worktreePattern = slash(cfgDir) + "-worktrees/{.branch}"
		path, err := renderWorktreePath(info, "feature")
		if err != nil {
			t.Fatalf("renderWorktreePath() error = %v, want a path: nothing of wt's is under there", err)
		}
		if want := filepath.Join(cfgDir+"-worktrees", "feature"); path != want {
			t.Errorf("renderWorktreePath() = %q, want %q", path, want)
		}
	})

	t.Run("through a dangling symlink to a dotfiles repo", func(t *testing.T) {
		// A ~/.config/wt symlinked into a dotfiles repo that has not been
		// cloned yet: EvalSymlinks gives up on the whole path, and backing off
		// past the link treats its name as an ordinary missing directory. It is
		// not one — putting files at the target is what makes the link live,
		// and wt would then be reading its config and approvals out of them.
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs a privilege we cannot assume on Windows")
		}
		linked := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(linked, ".config"))
		if err := os.MkdirAll(filepath.Join(linked, ".config"), 0o755); err != nil {
			t.Fatal(err)
		}
		// The dotfiles checkout itself is absent, so the link dangles.
		target := filepath.Join(linked, "dotfiles", "wt")
		if err := os.Symlink(target, configDir()); err != nil {
			t.Fatal(err)
		}
		configFilePath = filepath.Join(configDir(), "config.toml")
		t.Cleanup(func() { configFilePath = filepath.Join(cfgDir, "config.toml") })

		worktreePattern = slash(target)
		if path, err := renderWorktreePath(info, "payload"); err == nil {
			t.Fatalf("renderWorktreePath() = %q, want an error: creating that is what makes the config directory exist", path)
		}
	})

	t.Run("reached through a macOS firmlink", func(t *testing.T) {
		// The alias no resolution step removes: /Users/alice and
		// /System/Volumes/Data/Users/alice are one directory, and neither is a
		// symlink, so EvalSymlinks returns both unchanged. Prefixing the
		// committed pattern with the data volume was a one-line walk past a
		// comparison of names.
		if runtime.GOOS != "darwin" {
			t.Skip("firmlinks are a macOS volume layout")
		}
		// Checked against the home directory rather than the config directory:
		// neither ~/.config nor ~/.config/wt has been created here, which is the
		// fresh machine the attack wants anyway.
		alias := filepath.Join("/System/Volumes/Data", mustCanonical(t, cfgDir))
		if !sameDirectory(t, filepath.Join("/System/Volumes/Data", mustCanonical(t, home)), home) {
			t.Skipf("%s does not alias the home directory on this machine", alias)
		}

		worktreePattern = slash(alias)
		if path, err := renderWorktreePath(info, "payload"); err == nil {
			t.Fatalf("renderWorktreePath() = %q, want an error: that is the config directory by another name", path)
		}
	})

	t.Run("reached through a symlinked config directory", func(t *testing.T) {
		// A ~/.config symlinked into a dotfiles repo is an ordinary setup. The
		// pattern then names the target, matches nothing lexically, and the
		// files still arrive at ~/.config/wt.
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs a privilege we cannot assume on Windows")
		}
		linked := t.TempDir()
		realConfig := filepath.Join(linked, "dotfiles", "config")
		if err := os.MkdirAll(realConfig, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(realConfig, filepath.Join(linked, ".config")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(linked, ".config"))
		configFilePath = filepath.Join(configDir(), "config.toml")

		worktreePattern = slash(filepath.Join(realConfig, "wt"))
		if path, err := renderWorktreePath(info, "payload"); err == nil {
			t.Fatalf("renderWorktreePath() = %q, want an error: that is ~/.config/wt by another name", path)
		}
	})
}

// sameDirectory reports whether two paths name the same directory, which is the
// premise the firmlink subtest above needs and cannot assume.
func sameDirectory(t *testing.T, a, b string) bool {
	t.Helper()
	infoA, err := os.Stat(a)
	if err != nil {
		return false
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(infoA, infoB)
}

// TestSamePathTreeComparesIdentity pins the comparison that makes the firmlink
// subtest above possible to fix at all: two names for one directory, related by
// nothing a string comparison can see.
//
// Exercised with a symlink because that is the aliasing a test can create on any
// platform. wtStateAtPath resolves symlinks before it gets here, so this is not
// how the guard meets one in practice — the point is that samePathTree answers
// correctly without that help, which is what a firmlink or a bind mount leaves
// it with.
func TestSamePathTreeComparesIdentity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege we cannot assume on Windows")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(filepath.Join(real, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{
			// Neither leaf exists — the fresh-machine case, where the config
			// directory has not been created yet either.
			name: "the same missing leaf under two names for one directory",
			a:    filepath.Join(link, "wt"),
			b:    filepath.Join(real, "wt"),
			want: true,
		},
		{
			name: "one alias contains the other's leaf",
			a:    link,
			b:    filepath.Join(real, "wt"),
			want: true,
		},
		{
			// Both sides exist, to different depths: the shallower one holds the
			// deeper, and neither base is the other.
			name: "an alias above a directory that exists",
			a:    link,
			b:    filepath.Join(real, "deep"),
			want: true,
		},
		{
			name: "different leaves under one directory are unrelated",
			a:    filepath.Join(link, "wt"),
			b:    filepath.Join(real, "other"),
			want: false,
		},
		{
			name: "a neighbour that merely starts the same way",
			a:    real + "-worktrees",
			b:    filepath.Join(real, "wt"),
			want: false,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := samePathTree(tt.a, tt.b); got != tt.want {
				t.Errorf("samePathTree(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
			if got := samePathTree(tt.b, tt.a); got != tt.want {
				t.Errorf("samePathTree(%q, %q) = %v, want %v — the answer must not depend on the order", tt.b, tt.a, got, tt.want)
			}
		})
	}
}

// Tested directly rather than through foldPath, which only applies it on
// Windows: the rule is Win32's, but it has to be right wherever it is read.
func TestTrimWindowsPathComponents(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`c:\users\a\appdata\roaming\wt.`, `c:\users\a\appdata\roaming\wt`},
		{`c:\users\a\wt `, `c:\users\a\wt`},
		{`c:\users\a\wt. . \x`, `c:\users\a\wt\x`},
		{"c:/users/a/wt.", "c:/users/a/wt"},
		{`c:\users\a\wt`, `c:\users\a\wt`},
		// Dots that are the whole component are the relative components, not a
		// name someone put a dot on the end of.
		{`c:\users\..\a`, `c:\users\..\a`},
		{`.\wt.`, `.\wt`},
		{"", ""},
	}
	for _, tt := range cases {
		if got := trimWindowsPathComponents(tt.in); got != tt.want {
			t.Errorf("trimWindowsPathComponents(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCanonicalExistingPath(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("keeps a missing leaf as written", func(t *testing.T) {
		// The case the guard exists for: neither the worktree nor, on a fresh
		// machine, the config directory is there yet. EvalSymlinks refuses the
		// whole path for that; this must not.
		missing := filepath.Join(real, "not", "here")
		want := filepath.Join(mustCanonical(t, real), "not", "here")
		if got := mustCanonical(t, missing); got != want {
			t.Errorf("canonicalExistingPath(%q) = %q, want %q — the resolved parent with the missing tail kept", missing, got, want)
		}
	})

	t.Run("resolves the part that exists", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs a privilege we cannot assume on Windows")
		}
		link := filepath.Join(dir, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		got := mustCanonical(t, filepath.Join(link, "missing"))
		want := filepath.Join(mustCanonical(t, real), "missing")
		if got != want {
			t.Errorf("canonicalExistingPath() = %q, want %q", got, want)
		}
	})

	t.Run("follows a symlink whose target is missing", func(t *testing.T) {
		// EvalSymlinks fails on the whole path when the target is absent, and
		// backing off past the link would call it an ordinary missing
		// directory. The name stands for the target either way.
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs a privilege we cannot assume on Windows")
		}
		target := filepath.Join(dir, "not-cloned-yet", "wt")
		link := filepath.Join(dir, "dangling")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if got, want := mustCanonical(t, link), mustCanonical(t, target); got != want {
			t.Errorf("canonicalExistingPath(%q) = %q, want %q — the link names its target", link, got, want)
		}
	})

	t.Run("reports failure on a cycle of dangling symlinks", func(t *testing.T) {
		// Two links naming each other resolve to nothing. Handing back the path
		// as it stands would leave the guard comparing a name that matches
		// nothing, so the walk has to say it got nowhere.
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs a privilege we cannot assume on Windows")
		}
		a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
		if err := os.Symlink(b, a); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(a, b); err != nil {
			t.Fatal(err)
		}
		if got, ok := canonicalExistingPath(a); ok {
			t.Errorf("canonicalExistingPath(%q) = %q, true; want ok=false — a cycle names no directory", a, got)
		}
	})

	t.Run("reports failure on a chain longer than the budget", func(t *testing.T) {
		// Acyclic: nothing loops, the chain is simply longer than wt will walk.
		// Same answer as a cycle, and for the same reason — a path returned here
		// is one the guard compares and finds equal to nothing.
		if runtime.GOOS == "windows" {
			t.Skip("symlink creation needs a privilege we cannot assume on Windows")
		}
		chain := t.TempDir()
		// The far end is never created, so every link dangles and each costs a hop.
		prev := filepath.Join(chain, "end")
		for i := 0; i < 33; i++ {
			link := filepath.Join(chain, fmt.Sprintf("link%d", i))
			if err := os.Symlink(prev, link); err != nil {
				t.Fatal(err)
			}
			prev = link
		}
		if got, ok := canonicalExistingPath(prev); ok {
			t.Errorf("canonicalExistingPath(%q) = %q, true; want ok=false — the walk ran out of hops", prev, got)
		}
		// And the guard built on it refuses, rather than allowing a placement it
		// was unable to check.
		if owned := wtStateAtPath(prev); owned == "" {
			t.Error(`wtStateAtPath() = "", want a refusal: an unfollowable path is not a checked one`)
		}
	})

	t.Run("makes a relative path absolute", func(t *testing.T) {
		// isPathWithinRoot compares its two arguments against each other, so a
		// path left relative here would answer that question wrongly.
		if got := mustCanonical(t, "x"); !filepath.IsAbs(got) {
			t.Errorf("canonicalExistingPath(%q) = %q, want an absolute path", "x", got)
		}
	})
}

// mustCanonical is canonicalExistingPath where the test's premise is that the
// path resolves; failing there is the test being wrong, not the code.
func mustCanonical(t *testing.T, path string) string {
	t.Helper()
	resolved, ok := canonicalExistingPath(path)
	if !ok {
		t.Fatalf("canonicalExistingPath(%q) could not resolve", path)
	}
	return resolved
}

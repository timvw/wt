package cmd

import (
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
		alias := filepath.Join("/System/Volumes/Data", canonicalExistingPath(cfgDir))
		if !sameDirectory(t, filepath.Join("/System/Volumes/Data", canonicalExistingPath(home)), home) {
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
		want := filepath.Join(canonicalExistingPath(real), "not", "here")
		if got := canonicalExistingPath(missing); got != want {
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
		got := canonicalExistingPath(filepath.Join(link, "missing"))
		want := filepath.Join(canonicalExistingPath(real), "missing")
		if got != want {
			t.Errorf("canonicalExistingPath() = %q, want %q", got, want)
		}
	})

	t.Run("makes a relative path absolute", func(t *testing.T) {
		// isPathWithinRoot compares its two arguments against each other, so a
		// path left relative here would answer that question wrongly.
		if got := canonicalExistingPath("x"); !filepath.IsAbs(got) {
			t.Errorf("canonicalExistingPath(%q) = %q, want an absolute path", "x", got)
		}
	})
}

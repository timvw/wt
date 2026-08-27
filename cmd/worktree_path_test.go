package cmd

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// TestWorktreeMayNotBePlacedOnGitsOwnConfig: wt's gate is about hook commands,
// and core.hooksPath is a hook command under another name. A committed pattern
// rendering onto ~/.config/git checks a branch out over git's global
// configuration and a hooks directory beside it, and the next `git worktree
// add` — wt's own — runs what is in it. Nothing is approved and nothing is
// prompted for, and it fires in every repository afterwards.
func TestWorktreeMayNotBePlacedOnGitsOwnConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", "")

	for _, path := range []string{
		filepath.Join(home, ".config", "git"),
		filepath.Join(home, ".config", "git", "hooks"),
		// A worktree AT ~/.config plants a committed git/config underneath it
		// just as well, which is why the containment runs both ways.
		filepath.Join(home, ".config"),
		filepath.Join(home, ".gitconfig"),
		// os.Open is case-insensitive on macOS and Windows, and so is git.
		filepath.Join(home, ".config", "GIT"),
	} {
		if owned := wtStateAtPath(path); owned == "" {
			t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: a branch checked out there supplies "+
				"core.hooksPath and the programs it names", path)
		}
	}

	// And an ordinary path is still an ordinary path.
	if owned := wtStateAtPath(filepath.Join(home, ".config", "gitui")); owned != "" {
		t.Errorf("wtStateAtPath(~/.config/gitui) = %q, want \"\": guarding git's config directory "+
			"must not spread to every name that starts the same way", owned)
	}
}

// TestGitConfigGlobalIsGuardedWhenSet: GIT_CONFIG_GLOBAL replaces the defaults,
// so the file it names is the one that decides what git runs.
func TestGitConfigGlobalIsGuardedWhenSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	elsewhere := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", elsewhere)

	if owned := wtStateAtPath(elsewhere); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: GIT_CONFIG_GLOBAL names git's global config", elsewhere)
	}
	// A relative one is resolved by git against the working directory, so it
	// names a different file in every repository — including one a repository
	// committed. wt cannot guard a path that moves, and it did not open the
	// hole: the file is read the moment any git command runs there. It names no
	// path to protect, the defaults still stand, and the user is told.
	relativeGitEnvWarnings = sync.Map{}
	t.Cleanup(func() { relativeGitEnvWarnings = sync.Map{} })
	stderr := captureStderr(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "gitconfig")
	if owned := wtStateAtPath(filepath.Join(home, ".gitconfig")); owned == "" {
		t.Error(`wtStateAtPath(~/.gitconfig) = "", want a refusal even with a relative GIT_CONFIG_GLOBAL`)
	}
	if got := stderr(); !strings.Contains(got, "GIT_CONFIG_GLOBAL") {
		t.Errorf("nothing said about a relative GIT_CONFIG_GLOBAL; got %q.\n"+
			"wt cannot close that hole, so the one thing it must not do is imply the gate holds", got)
	}
}

// TestWorktreeMayNotBePlacedInTheRepositorysOwnGitDir: .git/hooks is empty on a
// clone made with no init template, `git worktree add` checks out into an empty
// directory, and git runs what it finds there on the very next worktree add.
// A branch whose tree is one executable post-checkout file is then arbitrary
// code, with the approval gate never consulted.
func TestWorktreeMayNotBePlacedInTheRepositorysOwnGitDir(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	t.Chdir(repo)

	for _, path := range []string{
		filepath.Join(repo, ".git", "hooks"),
		filepath.Join(repo, ".git"),
		filepath.Join(repo, ".git", "config"),
	} {
		if owned := wtStateAtPath(path); owned == "" {
			t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: a worktree there is the repository "+
				"writing its own .git, and git runs what lands in hooks/", path)
		}
	}

	// The repository's working tree is where worktrees legitimately go.
	if owned := wtStateAtPath(filepath.Join(repo, "sub")); owned != "" {
		t.Errorf("wtStateAtPath(%q/sub) = %q, want \"\"", repo, owned)
	}
}

// TestWorktreeMayNotBePlacedInAnyRepositorysGitDir: the mechanism does not care
// whose .git it is. A pattern naming ~/src/victim/.git/hooks reaches an empty
// directory on any clone made with no init template, and the next checkout in
// victim runs what was left there — with the attacker's repository nowhere near
// it.
func TestWorktreeMayNotBePlacedInAnyRepositorysGitDir(t *testing.T) {
	here := t.TempDir()
	runGit(t, here, "init", "-q")
	t.Chdir(here)

	elsewhere := t.TempDir()
	victim := filepath.Join(elsewhere, "src", "victim")
	if owned := wtStateAtPath(filepath.Join(victim, ".git", "hooks")); owned == "" {
		t.Error(`wtStateAtPath(<other repo>/.git/hooks) = "", want a refusal: guarding only the ` +
			`current repository's .git leaves every other repository on the machine open`)
	}

	// A bare-looking name is a directory like any other, and refusing every path
	// with "git" in it would take ordinary worktrees with it.
	for _, ok := range []string{
		filepath.Join(victim, "git", "hooks"),
		filepath.Join(elsewhere, "src", "victim.git"),
		filepath.Join(elsewhere, "gitconfig-tools"),
	} {
		if owned := wtStateAtPath(ok); owned != "" {
			t.Errorf("wtStateAtPath(%q) = %q, want \"\"", ok, owned)
		}
	}
}

// TestAGitDirReachedThroughASymlinkIsStillAGitDir: the pattern names the .git,
// but a symlink is what hides that it does.
func TestAGitDirReachedThroughASymlinkIsStillAGitDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege we cannot assume on Windows")
	}
	here := t.TempDir()
	runGit(t, here, "init", "-q")
	t.Chdir(here)

	victim := t.TempDir()
	runGit(t, victim, "init", "-q")
	link := filepath.Join(t.TempDir(), "innocent")
	if err := os.Symlink(filepath.Join(victim, ".git"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if owned := wtStateAtPath(filepath.Join(link, "hooks")); owned == "" {
		t.Error(`wtStateAtPath(<symlink to a .git>/hooks) = "", want a refusal: the name a pattern ` +
			`uses is not what git runs the hooks out of`)
	}
}

// TestCoreHooksPathIsGuardedWhereItPointsElsewhere: core.hooksPath is where git
// will look instead of .git/hooks, so a value naming a directory that does not
// exist yet is the same armed slot as an absent [include].
func TestCoreHooksPathIsGuardedWhereItPointsElsewhere(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	shared := filepath.Join(t.TempDir(), "githooks")
	runGit(t, repo, "config", "core.hooksPath", shared)
	t.Chdir(repo)

	if owned := wtStateAtPath(shared); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: core.hooksPath names it, so a branch "+
			"checked out there is what git runs", shared)
	}
}

// TestGitConfigIncludesAreGuarded: git ignores an [include] whose file is not
// there rather than complaining, so a path into dotfiles that have not been
// cloned is an armed slot. Filling it is supplying git's global config.
func TestGitConfigIncludesAreGuarded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// Unset, not empty: git reads an empty GIT_CONFIG_GLOBAL as "there is no
	// global config" and would report no includes at all. t.Setenv registers the
	// restore either way.
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	if err := os.Unsetenv("GIT_CONFIG_GLOBAL"); err != nil {
		t.Fatal(err)
	}

	dotfiles := filepath.Join(home, "dotfiles")
	config := "[include]\n\tpath = " + filepath.ToSlash(filepath.Join(dotfiles, "gitconfig")) + "\n" +
		"[includeIf \"gitdir:~/src/acme/\"]\n\tpath = " +
		filepath.ToSlash(filepath.Join(home, "work", "acme.inc")) + "\n" +
		// Relative, which git resolves against the directory of the file that
		// declared it — so this names ~/relative/gitconfig, and is exactly as
		// armed as the absolute spelling above.
		"[include]\n\tpath = relative/gitconfig\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(dotfiles, "gitconfig"),
		// Both directions: a worktree AT ~/dotfiles plants the gitconfig under it.
		dotfiles,
		// An includeIf is included in whichever repository matches its condition,
		// which is not a reason to leave the file it names open here.
		filepath.Join(home, "work", "acme.inc"),
		// Resolved against ~/.gitconfig's directory, not wt's cwd.
		filepath.Join(home, "relative", "gitconfig"),
		filepath.Join(home, "relative"),
	} {
		if owned := wtStateAtPath(path); owned == "" {
			t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: ~/.gitconfig includes it, so a "+
				"branch checked out there is read as git's global config", path)
		}
	}

	if owned := wtStateAtPath(filepath.Join(home, "src")); owned != "" {
		t.Errorf("wtStateAtPath(~/src) = %q, want \"\"", owned)
	}
}

// TestProcSelfCwdIsNotAnAbsoluteConfigPath: /proc/self/cwd is the working
// directory wearing an absolute spelling. It passes filepath.IsAbs, and it walks
// past every containment test, because those look for the repository in the
// path's parents and its parents are /proc/self and /proc. Stand in a
// subdirectory of a repository with WT_CONFIG=/proc/self/cwd/config.toml and a
// committed config.toml is read as yours — [trust] rules and all.
func TestProcSelfCwdIsNotAnAbsoluteConfigPath(t *testing.T) {
	for _, path := range []string{
		"/proc/self/cwd/config.toml",
		"/proc/self/root/etc/wt.toml",
		"/proc/thread-self/cwd/config.toml",
		// Clean() first, so the spelling with a detour is the same path.
		"/proc/self/../self/cwd/config.toml",
		// A numeric pid is the same trick aimed at another process: the shell
		// wt was launched from has its cwd inside the repository too, and
		// nothing about "self" was what made the first spelling wrong.
		"/proc/1234/cwd/config.toml",
		"/proc/1/root/etc/wt.toml",
	} {
		if !pathIsProcessRelative(path) {
			t.Errorf("pathIsProcessRelative(%q) = false; it names a different file per process", path)
		}
	}

	// /proc is not a magic prefix, and a name is not a pid for starting with a
	// digit.
	for _, path := range []string{
		"/proc/1234abc/cwd/config.toml",
		"/proc/cpuinfo",
		"/procession/self/cwd.toml",
		"/home/you/.config/wt/config.toml",
	} {
		if pathIsProcessRelative(path) {
			t.Errorf("pathIsProcessRelative(%q) = true; that names one file", path)
		}
	}
}

// TestAGitDirInAnotherCaseIsStillAGitDir: on a case-insensitive volume — macOS
// and Windows by default — ~/src/victim/.GIT/hooks IS ~/src/victim/.git/hooks,
// and a pattern is free to spell it either way. The guard refuses, so it folds.
func TestAGitDirInAnotherCaseIsStillAGitDir(t *testing.T) {
	for _, path := range []string{
		"/home/you/src/victim/.GIT/hooks",
		"/home/you/src/victim/.Git/hooks",
	} {
		if !pathInsideAGitDir(path) {
			t.Errorf("pathInsideAGitDir(%q) = false; a case-insensitive volume reads that as .git, "+
				"and a worktree there writes the hooks git runs", path)
		}
	}
	if pathInsideAGitDir("/home/you/src/gitignore/wt") {
		t.Error(`pathInsideAGitDir("/home/you/src/gitignore/wt") = true; ".gitignore" is not ".git"`)
	}
}

// TestGitTemplateDirAndFsmonitorAreGuarded: core.hooksPath is not the only
// setting naming a place git runs something from. init.templateDir's hooks/ is
// copied into every repository git creates afterwards — verified, not assumed —
// so filling it arms every future clone rather than one repository, and
// core.fsmonitor is a hook program git runs on any command that reads the index.
func TestGitTemplateDirAndFsmonitorAreGuarded(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	elsewhere := t.TempDir()
	template := filepath.Join(elsewhere, "git-template")
	fsmonitor := filepath.Join(elsewhere, "watchman", "query")
	runGit(t, repo, "config", "init.templateDir", template)
	runGit(t, repo, "config", "core.fsmonitor", fsmonitor)
	t.Chdir(repo)

	if owned := wtStateAtPath(filepath.Join(template, "hooks")); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: git copies that directory's hooks into "+
			"every repository it creates afterwards", filepath.Join(template, "hooks"))
	}
	// The parent, because the value names a file and a worktree is placed on a
	// directory: filling ~/watchman is what puts the program there.
	if owned := wtStateAtPath(filepath.Dir(fsmonitor)); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: core.fsmonitor names a program inside it, "+
			"which git runs on any command that reads the index", filepath.Dir(fsmonitor))
	}
}

// TestGitTemplateDirFromTheEnvironmentIsGuarded: GIT_TEMPLATE_DIR is
// init.templateDir needing no config file at all.
func TestGitTemplateDirFromTheEnvironmentIsGuarded(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	template := filepath.Join(t.TempDir(), "git-template")
	t.Setenv("GIT_TEMPLATE_DIR", template)
	t.Chdir(repo)

	if owned := wtStateAtPath(filepath.Join(template, "hooks")); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: GIT_TEMPLATE_DIR names it",
			filepath.Join(template, "hooks"))
	}
}

// TestExpandGitPathTakesTheTildeUserForm: git runs core.hooksPath through
// getpwnam and arrives at an absolute path — verified: `git -c
// core.hooksPath=~you/HOOKDIR rev-parse --git-path hooks` prints the expansion.
// wt reading the same value as relative would skip it, and skipping is not
// guarding.
func TestExpandGitPathTakesTheTildeUserForm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("~user needs a passwd entry to expand; git does not do it here and neither does wt")
	}
	me, err := user.Current()
	if err != nil || me.Username == "" {
		t.Skip("no current user to look up")
	}
	if _, err := user.Lookup(me.Username); err != nil {
		t.Skipf("this platform cannot look %s up by name: %v", me.Username, err)
	}
	got := expandGitPath("~" + me.Username + "/armed/hooks")
	if !filepath.IsAbs(got) {
		t.Errorf("expandGitPath(~%s/armed/hooks) = %q, want an absolute path; git expands that form, "+
			"so a value wt reads as relative is one it silently declines to guard", me.Username, got)
	}
	if got := expandGitPath("hooks"); got != "hooks" {
		t.Errorf("expandGitPath(hooks) = %q, want it left alone", got)
	}
}

// TestRelativeXdgConfigHomeIsReportedAsGitsGlobalConfig: wt ignores a
// non-absolute XDG_CONFIG_HOME per the XDG spec and falls back to ~/.config,
// while git honours it against the working directory — verified: with
// XDG_CONFIG_HOME=.xdg, git reads a committed .xdg/git/config. There is no
// placement to refuse, so the answer is to say so.
func TestRelativeXdgConfigHomeIsReportedAsGitsGlobalConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	relativeGitEnvWarnings = sync.Map{}
	t.Cleanup(func() { relativeGitEnvWarnings = sync.Map{} })
	stderr := captureStderr(t)
	t.Setenv("XDG_CONFIG_HOME", ".xdg")

	// The fallback still stands: a relative value names no path to protect, and
	// dropping the default would trade a warning for a hole.
	if owned := wtStateAtPath(filepath.Join(home, ".config", "git")); owned == "" {
		t.Error(`wtStateAtPath(~/.config/git) = "", want a refusal even with a relative XDG_CONFIG_HOME`)
	}
	// Asked for the git-specific sentence, not just the variable name: wt warns
	// about a non-absolute XDG_CONFIG_HOME anyway, to say it is ignoring it for
	// its own config directory. That one says nothing about git honouring the
	// same value, which is the whole of what is dangerous here — and a test
	// satisfied by the wrong warning would report this fix as present when it
	// had been removed. It did.
	got := stderr()
	if !strings.Contains(got, "XDG_CONFIG_HOME") || !strings.Contains(got, "git resolves it per directory") {
		t.Errorf("nothing said about git honouring a relative XDG_CONFIG_HOME; got %q.\n"+
			"git reads a repository's committed .xdg/git/config there, and wt cannot guard a path "+
			"that moves — but it can refuse to be quiet about it", got)
	}
}

// TestGitOutputPathKeepsATrailingSpace: on Unix a directory whose name ends in a
// space is a different directory from the one whose name does not. A .wt.toml
// writing `pattern = "{{.Root}}/{{.Branch}} "` puts a worktree at one of them,
// and TrimSpace would pin its approval to the scope of the other — the sibling
// the user keeps their real work in, whose hooks they have already approved.
func TestGitOutputPathKeepsATrailingSpace(t *testing.T) {
	if got := gitOutputPath([]byte("/home/you/src/tool \n")); got != "/home/you/src/tool " {
		t.Errorf("gitOutputPath(%q) = %q, want the trailing space kept: it is part of the name, and "+
			"dropping it makes two repositories answer to one approval", "/home/you/src/tool \n", got)
	}
	// And the carriage return, which is the same collision with a sharper edge:
	// git prints one when the path has one — verified with `git init` on a
	// directory whose name ends in CR — so taking it would give "tool\r" the
	// scope of its neighbour "tool", and approving one would approve the other.
	if got := gitOutputPath([]byte("/home/you/src/tool\r\n")); got != "/home/you/src/tool\r" {
		t.Errorf("gitOutputPath(%q) = %q, want the carriage return kept: git printed it because the "+
			"directory name has it, and two repositories must not answer to one approval",
			"/home/you/src/tool\r\n", got)
	}
}

// TestProcessRelativeXdgConfigHomeIsNotAConfigDir: XDG_CONFIG_HOME is where the
// trust store comes from, so a value meaning "here" makes wt read its record of
// what you have approved out of whatever repository you are standing in. A
// repository committing .config/wt/trust.toml with its own scope and hash then
// arrives pre-approved — the gate supplied rather than passed. Refusing the
// config file and not the directory beneath it closes the smaller half.
func TestProcessRelativeXdgConfigHomeIsNotAConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configHomeWarnings = sync.Map{}
	t.Cleanup(func() { configHomeWarnings = sync.Map{} })
	stderr := captureStderr(t)
	// APPDATA as well as HOME: on Windows that is the fallback configDir reaches
	// for first, and leaving it alone would have the test compare against the
	// runner's real one.
	want := filepath.Join(home, ".config", "wt")
	if runtime.GOOS == "windows" {
		appdata := filepath.Join(home, "AppData", "Roaming")
		t.Setenv("APPDATA", appdata)
		want = filepath.Join(appdata, "wt")
	}

	const procPath = "/proc/self/cwd/.config"
	t.Setenv("XDG_CONFIG_HOME", procPath)

	if got := configDir(); got != want {
		t.Errorf("configDir() = %q, want %q: /proc/self/cwd is absolute and still means the working "+
			"directory, so the trust store would come from the repository", got, want)
	}
	// The warning has to be true, or it is one the user learns to skip. Which
	// reason is the true one depends on the platform: a leading slash with no
	// drive letter is not an absolute path on Windows, so there the ordinary
	// "not absolute" answer is the correct one and the path never reaches the
	// process-relative test at all.
	said := stderr()
	reason := "depending on which process asks"
	if !filepath.IsAbs(procPath) {
		reason = "is not an absolute path"
	}
	if !strings.Contains(said, reason) {
		t.Errorf("configDir() said %q; want it to say %q — a warning the user can see is wrong is one "+
			"they learn to skip", said, reason)
	}
}

// TestABareRepositoryIsAGitDirectoryToo: the .git component test sees every
// repository cloned the ordinary way and no bare one. `git init --bare
// /srv/git/project` keeps its hooks at /srv/git/project/hooks, with nothing in
// the name to read — and an empty hooks/ is all `git worktree add` needs, which
// is the normal state of one on a server. Verified by hand before this was
// written: the checkout succeeds and leaves an executable hook behind.
func TestABareRepositoryIsAGitDirectoryToo(t *testing.T) {
	dir := t.TempDir()
	// No ".git" anywhere in the name, which is the point.
	bare := filepath.Join(dir, "srv", "project")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	// --template= so hooks/ comes out empty, as it does on a server whose
	// .sample files were never installed or have been removed.
	runGit(t, dir, "init", "-q", "--bare", "--template=", bare)

	hooks := filepath.Join(bare, "hooks")
	if got := wtStateAtPath(hooks); got == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: git checks a worktree out into an empty "+
			"hooks/ and the committed post-receive is what the next push runs", hooks)
	}
	// The repository itself, not only its hooks directory: everything in it is
	// git's, and a pattern may name any of it.
	if got := wtStateAtPath(filepath.Join(bare, "anything")); got == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal", filepath.Join(bare, "anything"))
	}
	// And an ordinary directory beside it still goes through, or the guard has
	// stopped being a guard and started being a wall.
	beside := filepath.Join(dir, "srv", "notarepo")
	if got := wtStateAtPath(beside); got != "" {
		t.Errorf("wtStateAtPath(%q) = %q, want \"\": it is a plain directory", beside, got)
	}
}

// TestAProcessRelativeGitConfigGlobalIsWarnedAboutForTheRightReason: a warning
// the user can see is wrong is a warning they learn to skip.
// GIT_CONFIG_GLOBAL=/proc/self/cwd/.gitconfig is refused, and it is not refused
// for being relative — it is as absolute as any other path.
func TestAProcessRelativeGitConfigGlobalIsWarnedAboutForTheRightReason(t *testing.T) {
	relativeGitEnvWarnings = sync.Map{}
	t.Cleanup(func() { relativeGitEnvWarnings = sync.Map{} })
	stderr := captureStderr(t)
	const procPath = "/proc/self/cwd/.gitconfig"
	t.Setenv("GIT_CONFIG_GLOBAL", procPath)

	gitGlobalConfigPaths()

	said := stderr()
	// Same platform split as the XDG test: without a drive letter this is not an
	// absolute path on Windows, so there the ordinary answer is the true one.
	reason := "depending on which process asks"
	if !filepath.IsAbs(procPath) {
		reason = "is a relative path"
	}
	if !strings.Contains(said, reason) {
		t.Errorf("gitGlobalConfigPaths() said %q; want it to say %q", said, reason)
	}
}

// TestAProcessRelativeHomeIsNotAConfigDir: the override refused and the default
// walking in behind it. XDG_CONFIG_HOME and APPDATA are asked whether they name
// one directory; HOME is what is left when they do not, and it is reached by the
// same environment. A repository can then commit .config/wt/trust.toml carrying
// its own scope and the hash of its own .wt.toml, and be approved before it is
// ever looked at — the config FILE being refused does not help, because the
// trust store is a separate file read by the same broken answer.
func TestAProcessRelativeHomeIsNotAConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	// Cleared, not set: on Windows this is what configDir reaches for before
	// HOME, and leaving it would answer with the runner's real one instead of
	// exercising the fallback.
	t.Setenv("APPDATA", "")
	const procHome = "/proc/self/cwd"
	t.Setenv("HOME", procHome)
	t.Setenv("USERPROFILE", procHome)

	if got := configDir(); got != "" {
		t.Errorf("configDir() = %q, want \"\": HOME=%s is absolute and still means the working "+
			"directory, so the trust store — and the approvals in it — would come from whatever "+
			"repository wt is standing in", got, procHome)
	}
}

// TestATrustRuleAnchoredOnAProcessRelativeHomeNamesNothing: the same home, one
// layer down. A "~/trees/*" whitelist entry is meant to name the user's own
// trees; under this home it names the repository. Reachable even with configDir
// refusing that home, because --config and WT_CONFIG name the file directly.
func TestATrustRuleAnchoredOnAProcessRelativeHomeNamesNothing(t *testing.T) {
	const procHome = "/proc/self/cwd"
	t.Setenv("HOME", procHome)
	t.Setenv("USERPROFILE", procHome)

	if got := expandTilde("~/trees/*"); got != "" {
		t.Errorf("expandTilde(\"~/trees/*\") = %q, want \"\": a whitelist rule that resolves against "+
			"the working directory whitelists whatever is checked out there", got)
	}
}

// TestProcessRelativeGitConfigGlobalIsReportedNotGuarded:
// GIT_CONFIG_GLOBAL=/proc/self/cwd/.gitconfig is the repository's own file
// wearing an absolute spelling. Guarding it would mean refusing to place a
// worktree on something the repository has already supplied; the honest answer
// is the one a relative value gets.
func TestProcessRelativeGitConfigGlobalIsReportedNotGuarded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	relativeGitEnvWarnings = sync.Map{}
	t.Cleanup(func() { relativeGitEnvWarnings = sync.Map{} })
	stderr := captureStderr(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/proc/self/cwd/.gitconfig")

	if owned := wtStateAtPath(filepath.Join(home, ".gitconfig")); owned == "" {
		t.Error(`wtStateAtPath(~/.gitconfig) = "", want a refusal: the defaults still stand`)
	}
	if got := stderr(); !strings.Contains(got, "GIT_CONFIG_GLOBAL") {
		t.Errorf("nothing said about a GIT_CONFIG_GLOBAL that means \"here\"; got %q", got)
	}
}

// TestNestedGitConfigIncludesAreGuarded: git turns include expansion OFF when a
// specific file is named, and --global names one. Without --includes only the
// top level is reported, so an [include] inside an included file named a path wt
// never heard about — armed, and invisible. Verified both ways.
func TestNestedGitConfigIncludesAreGuarded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// Unset, not empty: git reads an empty GIT_CONFIG_GLOBAL as "there is no
	// global config" and would report no includes at all.
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	if err := os.Unsetenv("GIT_CONFIG_GLOBAL"); err != nil {
		t.Fatal(err)
	}

	middle := filepath.Join(home, "middle.inc")
	top := "[include]\n\tpath = " + filepath.ToSlash(middle) + "\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(top), 0o644); err != nil {
		t.Fatal(err)
	}
	// Relative, so this also checks that a nested value resolves against the
	// file that declared it rather than against ~/.gitconfig.
	if err := os.WriteFile(middle, []byte("[include]\n\tpath = deep/gitconfig\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{filepath.Join(home, "deep", "gitconfig"), filepath.Join(home, "deep")} {
		if owned := wtStateAtPath(path); owned == "" {
			t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: an [include] two files deep names it, "+
				"and git ignores an include whose file is absent rather than complaining", path)
		}
	}
}

// TestTheTrustStoreIsGuardedWhereItPointsElsewhere: trust.toml and the directory
// holding it need not be in the same place. Guarding ~/.config/wt says nothing
// about where a symlink inside it points, and a dangling one — dotfiles not
// cloned yet — is a path a pattern can render onto. What gets checked out there
// is the record of what you have approved.
func TestTheTrustStoreIsGuardedWhereItPointsElsewhere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// XDG_CONFIG_HOME wins over APPDATA on every platform, so trustFilePath()
	// lands under the temporary home — but say so, since the directory is made
	// by hand here and the two must agree.
	if err := os.MkdirAll(filepath.Dir(trustFilePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	dotfiles := filepath.Join(home, "dotfiles")
	if err := os.Symlink(filepath.Join(dotfiles, "wt-trust.toml"), trustFilePath()); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	if owned := wtStateAtPath(dotfiles); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: trust.toml points inside it, so a branch "+
			"checked out there supplies wt's record of approved hooks", dotfiles)
	}
}

// TestTheXdgGitConfigFileIsGuardedWhereItPointsElsewhere: guarding a directory
// says nothing about where a symlink inside it points. ~/.config/git/config is
// as often a link into a dotfiles repository as trust.toml is, and a dangling one
// — dotfiles not cloned yet — is a path a pattern can render onto.
func TestTheXdgGitConfigFileIsGuardedWhereItPointsElsewhere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, ".config", "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	dotfiles := filepath.Join(home, "dotfiles")
	link := filepath.Join(home, ".config", "git", "config")
	if err := os.Symlink(filepath.Join(dotfiles, "gitconfig"), link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}

	if owned := wtStateAtPath(dotfiles); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: git's global config points inside it, so "+
			"a branch checked out there supplies core.hooksPath", dotfiles)
	}
}

// TestGitConfigSystemIsGuardedWhenSet: /etc/gitconfig is root's and not
// placeable, but GIT_CONFIG_SYSTEM redirects it, and a value under the user's own
// home is as fillable as any other. The settings it carries are the same ones.
func TestGitConfigSystemIsGuardedWhenSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	elsewhere := filepath.Join(t.TempDir(), "armed", "config")
	t.Setenv("GIT_CONFIG_SYSTEM", elsewhere)

	if owned := wtStateAtPath(elsewhere); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: GIT_CONFIG_SYSTEM names git's system "+
			"config, which carries core.hooksPath like any other scope", elsewhere)
	}
}

// TestARelativeCoreHooksPathThatLeavesTheRepositoryIsGuarded: git resolves
// core.hooksPath against the top of the working tree, and "../shared-hooks"
// leaves it from there — so "relative means inside the repository, which a
// worktree cannot be placed in anyway" was not true.
func TestARelativeCoreHooksPathThatLeavesTheRepositoryIsGuarded(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "tool")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "config", "core.hooksPath", "../shared-hooks")
	t.Chdir(repo)

	shared := filepath.Join(parent, "shared-hooks")
	if owned := wtStateAtPath(shared); owned == "" {
		t.Errorf("wtStateAtPath(%q) = \"\", want a refusal: core.hooksPath reaches it from the top of "+
			"the working tree, and git runs what is checked out there", shared)
	}
}

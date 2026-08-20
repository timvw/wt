package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/timvw/wt/internal/fileops"
)

// isolateFileConfig points loadWorktreeConfig at a scratch environment so the
// developer's own ~/.config/wt/config.toml and ~/.gitconfig cannot influence
// the result, and restores every global it touches afterwards.
func isolateFileConfig(t *testing.T) {
	t.Helper()

	origCopy, origLink, origExclude := filesCopy, filesLink, filesExclude
	origIgnored, origSources := filesCopyIgnored, configSources
	origGitConfigFn, origGitRepoRootFn := gitConfigFn, gitRepoRootFn
	origConfigFlag := configFlag
	t.Cleanup(func() {
		filesCopy, filesLink, filesExclude = origCopy, origLink, origExclude
		filesCopyIgnored, configSources = origIgnored, origSources
		gitConfigFn, gitRepoRootFn = origGitConfigFn, origGitRepoRootFn
		configFlag = origConfigFlag
	})

	gitConfigFn = func(gitConfigScope) map[string]string { return nil }
	gitRepoRootFn = func() (string, error) { return "", os.ErrNotExist }
	configFlag = ""
	t.Setenv("WT_CONFIG", "/nonexistent/config.toml")
	t.Setenv("WORKTREE_ROOT", "")
	t.Setenv("WORKTREE_STRATEGY", "")
	t.Setenv("WORKTREE_PATTERN", "")
	os.Unsetenv("WT_COPY_IGNORED")
	os.Unsetenv("WT_FILES_DISABLED")
}

func patternsFrom(list []layeredPattern) []string {
	out := make([]string, 0, len(list))
	for _, p := range list {
		out = append(out, p.Pattern+"@"+p.Source)
	}
	return out
}

func TestFilePatternsAccumulateAcrossLayers(t *testing.T) {
	isolateFileConfig(t)

	tmp := t.TempDir()
	globalCfg := filepath.Join(tmp, "config.toml")
	writeFile(t, globalCfg, `[files]
copy = [".env", "shared.conf"]
exclude = ["*.pem"]
link = ["node_modules"]
`)

	repoDir := newFilesRepo(t, "")
	writeFile(t, filepath.Join(repoDir, ".wt.toml"), `[files]
copy = ["config/local.yml", ".env"]
exclude = ["*.key"]
`)
	// The include file layer comes last and is unioned into copy only.
	writeFile(t, filepath.Join(repoDir, worktreeIncludeFile), "shared.conf\n.envrc\n")

	t.Setenv("WT_CONFIG", globalCfg)
	gitRepoRootFn = func() (string, error) { return repoDir, nil }
	loadWorktreeConfig()

	cfg, err := resolveFileConfig(repoDir)
	if err != nil {
		t.Fatalf("resolveFileConfig: %v", err)
	}

	// A repo config must add to the user's config, never replace it, and a
	// pattern seen twice keeps its first-seen position and source.
	wantCopy := []string{
		".env@config file",
		"shared.conf@config file",
		"config/local.yml@repo config",
		".envrc@" + worktreeIncludeFile,
	}
	if got := patternsFrom(cfg.Copy); !equalStrings(got, wantCopy) {
		t.Errorf("copy = %v, want %v", got, wantCopy)
	}

	wantExclude := []string{"*.pem@config file", "*.key@repo config"}
	if got := patternsFrom(cfg.Exclude); !equalStrings(got, wantExclude) {
		t.Errorf("exclude = %v, want %v", got, wantExclude)
	}

	wantLink := []string{"node_modules@config file"}
	if got := patternsFrom(cfg.Link); !equalStrings(got, wantLink) {
		t.Errorf("link = %v, want %v", got, wantLink)
	}

	if !cfg.IncludeFileFound {
		t.Error("IncludeFileFound = false, want true")
	}
}

// exclude is applied last and cannot be overridden by a later layer's copy
// entry — a global "never copy *.pem" has to hold against any repository.
func TestExcludeIsAppliedLastAndNotOverridable(t *testing.T) {
	src := newFilesRepo(t, "*.pem\n*.env\n")
	writeFile(t, filepath.Join(src, "server.pem"), "PRIVATE KEY")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")

	// The repo asks for everything; the user's exclude still wins.
	setFileConfig(t, []string{"*.pem", "*.env"}, nil, []string{"*.pem"}, false)

	files := planPaths(t, src)
	if contains(files, "server.pem") {
		t.Errorf("excluded pattern was copied: %v", files)
	}
	if !contains(files, "app.env") {
		t.Errorf("app.env missing from plan: %v", files)
	}
}

// A trailing space is significant in gitignore syntax when escaped, so the
// config layer must hand patterns to the ignore parser verbatim rather than
// trimming them into something else.
func TestEscapedTrailingSpaceSurvivesTheConfigLayer(t *testing.T) {
	isolateFileConfig(t)

	tmp := t.TempDir()
	globalCfg := filepath.Join(tmp, "config.toml")
	// TOML "\\ " is the two characters backslash-space, i.e. gitignore's escape.
	writeFile(t, globalCfg, "[files]\ncopy = [\"trailing\\\\ \", \"plain  \"]\n")

	repoDir := newFilesRepo(t, "")
	t.Setenv("WT_CONFIG", globalCfg)
	gitRepoRootFn = func() (string, error) { return repoDir, nil }
	loadWorktreeConfig()

	cfg, err := resolveFileConfig(repoDir)
	if err != nil {
		t.Fatalf("resolveFileConfig: %v", err)
	}

	want := []string{`trailing\ `, "plain  "}
	if got := patternStrings(cfg.Copy); !equalStrings(got, want) {
		t.Fatalf("copy = %q, want %q", got, want)
	}

	// And the pattern must match the name it actually spells.
	if !cfg.copyMatcher.Match("trailing ", false) {
		t.Error(`pattern "trailing\ " does not match the file "trailing "`)
	}
	if cfg.copyMatcher.Match("trailing", false) {
		t.Error(`pattern "trailing\ " must not match "trailing"`)
	}
}

// A directory pattern in exclude covers everything below it. git reports
// candidates as leaf paths whenever a directory is not wholly ignored, so
// matching only the leaf would let "secrets/" miss "secrets/key.pem".
func TestDirectoryExcludeCoversItsContents(t *testing.T) {
	src := newFilesRepo(t, "*.pem\n")
	writeFile(t, filepath.Join(src, "secrets", "key.pem"), "PRIVATE")
	writeFile(t, filepath.Join(src, "keep.pem"), "ok")

	setFileConfig(t, nil, nil, []string{"secrets/"}, true)

	files := planPaths(t, src)
	if contains(files, "secrets/key.pem") {
		t.Errorf("a file below an excluded directory was selected: %v", files)
	}
	if !contains(files, "keep.pem") {
		t.Errorf("keep.pem missing from plan: %v", files)
	}
}

// A "!" in copy names a path the user does not want, so it has to win over the
// blanket yes of a matched parent directory or copy_ignored. This is a
// deliberate divergence from git, which cannot re-include below an excluded
// directory; it only ever copies less.
func TestNegatedCopyPatternWinsOverABlanketYes(t *testing.T) {
	t.Run("below a matched directory", func(t *testing.T) {
		src := newFilesRepo(t, "cache/\n")
		writeFile(t, filepath.Join(src, "cache", "a.txt"), "a")
		writeFile(t, filepath.Join(src, "cache", "private.key"), "SECRET")

		setFileConfig(t, []string{"cache/", "!cache/private.key"}, nil, nil, false)

		files := planPaths(t, src)
		if contains(files, "cache/private.key") {
			t.Errorf("negated pattern was selected anyway: %v", files)
		}
		if !contains(files, "cache/a.txt") {
			t.Errorf("cache/a.txt missing from plan: %v", files)
		}
	})

	t.Run("under copy_ignored", func(t *testing.T) {
		src := newFilesRepo(t, "*.key\n*.txt\n")
		writeFile(t, filepath.Join(src, "a.txt"), "a")
		writeFile(t, filepath.Join(src, "private.key"), "SECRET")

		setFileConfig(t, []string{"!private.key"}, nil, nil, true)

		files := planPaths(t, src)
		if contains(files, "private.key") {
			t.Errorf("negated pattern was selected despite copy_ignored: %v", files)
		}
		if !contains(files, "a.txt") {
			t.Errorf("a.txt missing from plan: %v", files)
		}
	})
}

// "applied last and not overridable" has to cover link too, or an exclude that
// looks effective for copies would be quietly ignored for links.
func TestExcludeAlsoAppliesToLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "node_modules/\n.venv/\n")
	writeFile(t, filepath.Join(src, "node_modules", "pkg", "index.js"), "{}")
	writeFile(t, filepath.Join(src, ".venv", "pyvenv.cfg"), "home = /usr")

	dst := newDestination(t)
	setFileConfig(t, nil, []string{"node_modules", ".venv"}, []string{".venv"}, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Linked != 1 {
		t.Fatalf("linked = %d, want 1 (%+v)", result.Summary.Linked, result.Files)
	}
	if _, err := os.Lstat(filepath.Join(dst, ".venv")); !os.IsNotExist(err) {
		t.Error("an excluded path was linked")
	}
	if _, err := os.Lstat(filepath.Join(dst, "node_modules")); err != nil {
		t.Errorf("node_modules was not linked: %v", err)
	}
}

// exclude accumulates with the repo's .wt.toml applied after the user's own
// config, so a committed "!*.pem" would undo exactly what a global exclude was
// protecting. Negation is refused in exclude and link for that reason.
func TestNegationIsRefusedInExcludeAndLink(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "exclude",
			config: "[files]\nexclude = [\"*.pem\", \"!keep.pem\"]\n",
			want:   `"!keep.pem"`,
		},
		{
			name:   "link",
			config: "[files]\nlink = [\"!node_modules\"]\n",
			want:   `"!node_modules"`,
		},
		{
			name:   "repo config undoing a global exclude",
			config: "[files]\nexclude = [\"!*.pem\"]\ncopy_ignored = true\n",
			want:   `"!*.pem"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateFileConfig(t)

			globalCfg := filepath.Join(t.TempDir(), "config.toml")
			writeFile(t, globalCfg, tt.config)

			repoDir := newFilesRepo(t, "")
			t.Setenv("WT_CONFIG", globalCfg)
			gitRepoRootFn = func() (string, error) { return repoDir, nil }
			loadWorktreeConfig()

			_, err := resolveFileConfig(repoDir)
			if err == nil {
				t.Fatal("expected a negation in the list to be refused")
			}
			if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "negation") {
				t.Errorf("error = %q, want it to name %s as a negation", err, tt.want)
			}
		})
	}
}

// A "!" is still legal in copy, where it means "not this one".
func TestNegationIsAllowedInCopy(t *testing.T) {
	isolateFileConfig(t)

	globalCfg := filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, globalCfg, "[files]\ncopy = [\"*.env\", \"!secret.env\"]\n")

	repoDir := newFilesRepo(t, "")
	t.Setenv("WT_CONFIG", globalCfg)
	gitRepoRootFn = func() (string, error) { return repoDir, nil }
	loadWorktreeConfig()

	if _, err := resolveFileConfig(repoDir); err != nil {
		t.Fatalf("resolveFileConfig: %v", err)
	}
}

// exclude also wins over copy_ignored, which is the blunt "copy everything"
// switch.
func TestExcludeOverridesCopyIgnored(t *testing.T) {
	src := newFilesRepo(t, "*.pem\n*.env\n")
	writeFile(t, filepath.Join(src, "server.pem"), "PRIVATE KEY")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")

	setFileConfig(t, nil, nil, []string{"*.pem"}, true)

	files := planPaths(t, src)
	if contains(files, "server.pem") {
		t.Errorf("excluded pattern was copied despite copy_ignored: %v", files)
	}
	if !contains(files, "app.env") {
		t.Errorf("app.env missing from plan: %v", files)
	}
}

func TestCopyIgnoredPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		global     map[string]string // global git config
		configFile string            // TOML written to the wt config file
		repoConfig string            // TOML written to .wt.toml
		local      map[string]string // local git config
		env        string            // WT_COPY_IGNORED, "" means unset
		want       bool
		wantSource string
	}{
		{
			name:       "default is false",
			want:       false,
			wantSource: "default",
		},
		{
			name:       "global git config",
			global:     map[string]string{"wt.copyignored": "true"},
			want:       true,
			wantSource: "git config (global)",
		},
		{
			name:       "config file beats global git config",
			global:     map[string]string{"wt.copyignored": "true"},
			configFile: "[files]\ncopy_ignored = false\n",
			want:       false,
			wantSource: "config file",
		},
		{
			name:       "repo config beats config file",
			configFile: "[files]\ncopy_ignored = false\n",
			repoConfig: "[files]\ncopy_ignored = true\n",
			want:       true,
			wantSource: "repo config",
		},
		{
			name:       "local git config beats repo config",
			repoConfig: "[files]\ncopy_ignored = true\n",
			local:      map[string]string{"wt.copyignored": "false"},
			want:       false,
			wantSource: "git config (local)",
		},
		{
			name:       "env beats everything",
			local:      map[string]string{"wt.copyignored": "false"},
			env:        "true",
			want:       true,
			wantSource: "env: WT_COPY_IGNORED",
		},
		{
			// copy_ignored = false must be distinguishable from unset, so a
			// repo can turn off a user's global "copy everything".
			name:       "explicit false is not treated as unset",
			global:     map[string]string{"wt.copyignored": "true"},
			repoConfig: "[files]\ncopy_ignored = false\n",
			want:       false,
			wantSource: "repo config",
		},
		{
			// git spells booleans several ways; an unparseable value is
			// ignored rather than silently read as false.
			name:       "unparseable git value is ignored",
			global:     map[string]string{"wt.copyignored": "maybe"},
			want:       false,
			wantSource: "default",
		},
		{
			name:       "git config yes/on spellings",
			global:     map[string]string{"wt.copyignored": "yes"},
			want:       true,
			wantSource: "git config (global)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateFileConfig(t)
			tmp := t.TempDir()

			if tt.configFile != "" {
				path := filepath.Join(tmp, "config.toml")
				writeFile(t, path, tt.configFile)
				t.Setenv("WT_CONFIG", path)
			}

			repoDir := filepath.Join(tmp, "repo")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.repoConfig != "" {
				writeFile(t, filepath.Join(repoDir, ".wt.toml"), tt.repoConfig)
			}
			gitRepoRootFn = func() (string, error) { return repoDir, nil }
			gitConfigFn = func(scope gitConfigScope) map[string]string {
				if scope == gitScopeLocal {
					return tt.local
				}
				return tt.global
			}
			if tt.env != "" {
				t.Setenv("WT_COPY_IGNORED", tt.env)
			}

			loadWorktreeConfig()

			if filesCopyIgnored != tt.want {
				t.Errorf("filesCopyIgnored = %v, want %v", filesCopyIgnored, tt.want)
			}
			if configSources.CopyIgnored != tt.wantSource {
				t.Errorf("source = %q, want %q", configSources.CopyIgnored, tt.wantSource)
			}
		})
	}
}

// The list keys are deliberately not readable from git config: they would need
// --get-all handling and have no accumulation story across git scopes.
func TestListKeysAreNotReadFromGitConfig(t *testing.T) {
	isolateFileConfig(t)

	gitConfigFn = func(gitConfigScope) map[string]string {
		return map[string]string{"wt.copy": ".env", "wt.files_copy": ".env"}
	}
	loadWorktreeConfig()

	if len(filesCopy) != 0 {
		t.Errorf("filesCopy = %v, want empty", filesCopy)
	}
}

func TestWorktreeIncludeUnionsIntoCopy(t *testing.T) {
	src := newFilesRepo(t, "*.env\nbuild/\n")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")
	writeFile(t, filepath.Join(src, "build", "out.bin"), "binary")
	writeFile(t, filepath.Join(src, worktreeIncludeFile), "# comment\n\napp.env\n")

	setFileConfig(t, nil, nil, nil, false)

	cfg, err := resolveFileConfig(src)
	if err != nil {
		t.Fatalf("resolveFileConfig: %v", err)
	}
	if got := patternsFrom(cfg.Copy); !equalStrings(got, []string{"app.env@" + worktreeIncludeFile}) {
		t.Fatalf("copy = %v", got)
	}

	files := planPaths(t, src)
	if !contains(files, "app.env") || contains(files, "build/out.bin") {
		t.Errorf("plan = %v, want only app.env", files)
	}
}

// An empty .worktreeinclude is a normal state, not an error.
func TestEmptyWorktreeIncludeIsNotAnError(t *testing.T) {
	src := newFilesRepo(t, "*.env\n")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")
	writeFile(t, filepath.Join(src, worktreeIncludeFile), "\n# only comments\n\n")

	setFileConfig(t, nil, nil, nil, false)

	cfg, err := resolveFileConfig(src)
	if err != nil {
		t.Fatalf("resolveFileConfig: %v", err)
	}
	if !cfg.IncludeFileFound {
		t.Error("IncludeFileFound = false, want true")
	}
	if len(cfg.Copy) != 0 {
		t.Errorf("copy = %v, want empty", cfg.Copy)
	}
	if cfg.configured() {
		t.Error("configured() = true with nothing to do")
	}
}

// A missing .worktreeinclude is the common case and must not error either.
func TestMissingWorktreeIncludeIsNotAnError(t *testing.T) {
	src := newFilesRepo(t, "")
	setFileConfig(t, nil, nil, nil, false)

	cfg, err := resolveFileConfig(src)
	if err != nil {
		t.Fatalf("resolveFileConfig: %v", err)
	}
	if cfg.IncludeFileFound {
		t.Error("IncludeFileFound = true for a repo without the file")
	}
}

// The inside-dotdir strategy puts worktrees under an ignored directory inside
// the repo. Copying it would recursively duplicate every worktree.
func TestNestedRegisteredWorktreeIsSkipped(t *testing.T) {
	src := newFilesRepo(t, ".worktrees/\n")
	runGitCommand(t, src, "worktree", "add", filepath.Join(src, ".worktrees", "feat"), "-b", "feat")
	writeFile(t, filepath.Join(src, ".worktrees", "loose.txt"), "not a worktree")

	// registeredWorktreePaths asks git about the current directory.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(src); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	setFileConfig(t, nil, nil, nil, true)
	files := planPaths(t, src)

	for _, f := range files {
		if strings.HasPrefix(f, ".worktrees/feat") {
			t.Fatalf("registered worktree was selected for copy: %v", files)
		}
	}
	// A non-worktree file in the same ignored directory is still copied.
	if !contains(files, ".worktrees/loose.txt") {
		t.Errorf(".worktrees/loose.txt missing from plan: %v", files)
	}
}

func TestBrokenSymlinkIsCopiedAsBrokenSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "dangling\n")
	if err := os.Symlink("nowhere/at/all", filepath.Join(src, "dangling")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	dst := newDestination(t)
	setFileConfig(t, []string{"dangling"}, nil, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Copied != 1 || result.Summary.Failed != 0 {
		t.Fatalf("summary = %+v, want 1 copied and 0 failed", result.Summary)
	}
	target, err := os.Readlink(filepath.Join(dst, "dangling"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "nowhere/at/all" {
		t.Errorf("target = %q, want %q", target, "nowhere/at/all")
	}
}

func TestMissingDestinationParentIsCreated(t *testing.T) {
	src := newFilesRepo(t, "config/\n")
	writeFile(t, filepath.Join(src, "config", "nested", "local.yml"), "key: value")

	dst := newDestination(t)
	setFileConfig(t, []string{"config"}, nil, nil, false)

	if _, err := runFileCopy(src, dst, copyOptions{}); err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}

	info, err := os.Stat(filepath.Join(dst, "config", "nested"))
	if err != nil {
		t.Fatalf("stat nested dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("nested destination parent is not a directory")
	}
	if got := readFile(t, filepath.Join(dst, "config", "nested", "local.yml")); got != "key: value" {
		t.Errorf("content = %q", got)
	}
}

func TestZeroByteFileIsCopied(t *testing.T) {
	src := newFilesRepo(t, "*.env\n")
	writeFile(t, filepath.Join(src, "empty.env"), "")

	dst := newDestination(t)
	setFileConfig(t, []string{"*.env"}, nil, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Copied != 1 {
		t.Fatalf("summary = %+v, want 1 copied", result.Summary)
	}
	info, err := os.Stat(filepath.Join(dst, "empty.env"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("size = %d, want 0", info.Size())
	}
}

// An unreadable file is reported as failed; the rest of the run continues.
func TestUnreadableFileFailsWithoutAbortingTheRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read any file regardless of mode")
	}

	src := newFilesRepo(t, "*.env\n")
	secret := filepath.Join(src, "secret.env")
	writeFile(t, secret, "TOKEN=1")
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })
	writeFile(t, filepath.Join(src, "readable.env"), "OK=1")

	dst := newDestination(t)
	setFileConfig(t, []string{"*.env"}, nil, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy should not abort on an unreadable file: %v", err)
	}
	if result.Summary.Failed != 1 {
		t.Errorf("failed = %d, want 1 (%+v)", result.Summary.Failed, result.Files)
	}
	if result.Summary.Copied != 1 {
		t.Errorf("copied = %d, want 1 (%+v)", result.Summary.Copied, result.Files)
	}
	if got := readFile(t, filepath.Join(dst, "readable.env")); got != "OK=1" {
		t.Errorf("readable.env content = %q", got)
	}
}

// --force promises to update a file, not to lose it: a copy that fails partway
// must leave what was already there untouched.
func TestForceKeepsTheDestinationWhenTheCopyFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can read any file regardless of mode")
	}

	src := newFilesRepo(t, "*.env\n")
	secret := filepath.Join(src, "secret.env")
	writeFile(t, secret, "NEW=1")
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })

	dst := newDestination(t)
	writeFile(t, filepath.Join(dst, "secret.env"), "OLD=1")

	setFileConfig(t, []string{"*.env"}, nil, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{Force: true})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Failed != 1 {
		t.Errorf("failed = %d, want 1 (%+v)", result.Summary.Failed, result.Files)
	}
	if got := readFile(t, filepath.Join(dst, "secret.env")); got != "OLD=1" {
		t.Errorf("secret.env = %q, want the original OLD=1 to have survived", got)
	}
	// And no half-written temporary is left lying around.
	if entries := listTree(t, dst); !equalStrings(entries, []string{".", "secret.env"}) {
		t.Errorf("destination = %v, want just secret.env", entries)
	}
}

// The read-only probe has to give the same answer as the write probe it stands
// in for, whatever this machine's filesystem happens to be — and it has to do
// so without writing, on an empty destination as much as on a populated one.
func TestCaseInsensitiveWorktreeAgreesWithTheWriteProbe(t *testing.T) {
	empty := newDestination(t)
	populated := newDestination(t)
	writeFile(t, filepath.Join(populated, "App.env"), "TOKEN=1")

	for _, dst := range []string{empty, populated} {
		if got, want := caseInsensitiveWorktree(dst), filesystemCaseInsensitive(dst); got != want {
			t.Errorf("%s: caseInsensitiveWorktree = %v, filesystemCaseInsensitive = %v", dst, got, want)
		}

		before := listTree(t, dst)
		caseInsensitiveWorktree(dst)
		if after := listTree(t, dst); !equalStrings(before, after) {
			t.Errorf("%s: the probe wrote to the worktree:\nbefore %v\nafter  %v", dst, before, after)
		}
	}
}

func TestCaseInsensitiveCollisionIsReportedNotOverwritten(t *testing.T) {
	dst := newDestination(t)
	if !filesystemCaseInsensitive(dst) {
		t.Skip("filesystem is case-sensitive; nothing can collide")
	}

	kept, dropped := dropCaseCollisions(dst, []string{".env", ".ENV", "other"})

	if !equalStrings(kept, []string{".env", "other"}) {
		t.Errorf("kept = %v, want [.env other]", kept)
	}
	if len(dropped) != 1 {
		t.Fatalf("dropped = %v, want one entry", dropped)
	}
	if dropped[0].Action != fileActionSkipped {
		t.Errorf("action = %q, want %q", dropped[0].Action, fileActionSkipped)
	}
	if !strings.Contains(dropped[0].Reason, "case-insensitive") {
		t.Errorf("reason = %q, want it to explain the collision", dropped[0].Reason)
	}
}

func TestDropCaseCollisionsIsANoOpOnCaseSensitiveFS(t *testing.T) {
	dst := newDestination(t)
	if filesystemCaseInsensitive(dst) {
		t.Skip("filesystem is case-insensitive")
	}

	kept, dropped := dropCaseCollisions(dst, []string{".env", ".ENV"})
	if !equalStrings(kept, []string{".env", ".ENV"}) || dropped != nil {
		t.Errorf("kept = %v, dropped = %v; both paths should survive", kept, dropped)
	}
}

// Progress must appear only past the thresholds, and never under --format json
// where stdout has to stay parseable and stderr noise is unwanted.
func TestProgressReporterThresholds(t *testing.T) {
	origFormat := outputFormat
	t.Cleanup(func() { outputFormat = origFormat })

	tests := []struct {
		name   string
		files  int
		bytes  int64
		format string
		want   bool
	}{
		{name: "small copy stays silent", files: 10, bytes: 1024, want: false},
		{name: "at the file threshold stays silent", files: progressFileThreshold, bytes: 0, want: false},
		{name: "past the file threshold reports", files: progressFileThreshold + 1, bytes: 0, want: true},
		{name: "past the byte threshold reports", files: 2, bytes: progressByteThreshold + 1, want: true},
		{name: "json mode stays silent", files: 50_000, bytes: progressByteThreshold * 4, format: "json", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outputFormat = tt.format
			p := newProgressReporter(tt.files, tt.bytes)
			if p.enabled != tt.want {
				t.Errorf("enabled = %v, want %v", p.enabled, tt.want)
			}
		})
	}
}

func TestHumanBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1 << 20, "1.0 MiB"},
		{1 << 30, "1.0 GiB"},
		{1 << 40, "1.0 TiB"},
	}
	for _, tt := range tests {
		if got := humanBytes(tt.in); got != tt.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// WT_FILES_DISABLED must suppress everything, mirroring WT_HOOKS_DISABLED.
func TestFilesDisabledSuppressesTheCopy(t *testing.T) {
	src := newFilesRepo(t, "*.env\n")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")
	dst := newDestination(t)

	setFileConfig(t, []string{"*.env"}, nil, nil, false)
	t.Setenv("WT_FILES_DISABLED", "1")

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if len(result.Files) != 0 {
		t.Errorf("files = %v, want none", result.Files)
	}
	if _, err := os.Lstat(filepath.Join(dst, "app.env")); !os.IsNotExist(err) {
		t.Error("app.env was copied despite WT_FILES_DISABLED=1")
	}
}

// A dry run must predict exactly what the real run does, and touch nothing.
func TestDryRunMatchesTheRealRun(t *testing.T) {
	src := newFilesRepo(t, "*.env\n")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")
	writeFile(t, filepath.Join(src, "other.env"), "OTHER=1")
	dst := newDestination(t)
	writeFile(t, filepath.Join(dst, "other.env"), "EXISTING=1")

	setFileConfig(t, []string{"*.env"}, nil, nil, false)

	beforeSrc := listTree(t, src)
	beforeDst := listTree(t, dst)

	dry, err := runFileCopy(src, dst, copyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "app.env")); !os.IsNotExist(err) {
		t.Fatal("the dry run created a file")
	}
	// "Change nothing" covers both trees. The source may well be read-only, so
	// neither the reflink probe nor the case-sensitivity probe may write there
	// — and the destination has to come out byte-identical as well.
	if after := listTree(t, src); !equalStrings(beforeSrc, after) {
		t.Errorf("the dry run touched the source worktree:\nbefore %v\nafter  %v", beforeSrc, after)
	}
	if after := listTree(t, dst); !equalStrings(beforeDst, after) {
		t.Errorf("the dry run touched the destination worktree:\nbefore %v\nafter  %v", beforeDst, after)
	}

	real, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("real run: %v", err)
	}

	if len(dry.Files) != len(real.Files) {
		t.Fatalf("dry run planned %d entries, real run produced %d", len(dry.Files), len(real.Files))
	}
	for i := range dry.Files {
		if dry.Files[i].Path != real.Files[i].Path || dry.Files[i].Action != real.Files[i].Action {
			t.Errorf("entry %d: dry %+v, real %+v", i, dry.Files[i], real.Files[i])
		}
		// The predicted method has to match too, or --dry-run misreports
		// whether a copy will be a cheap reflink.
		if dry.Files[i].Method != real.Files[i].Method {
			t.Errorf("entry %d: method dry %q, real %q", i, dry.Files[i].Method, real.Files[i].Method)
		}
	}
}

// A path can end up in both lists — a blanket "*.env" in copy and an explicit
// entry in link. The real run copies first, so the link then finds its
// destination taken; the dry run writes nothing and has to reach the same
// conclusion on its own rather than promising both a copy and a link.
func TestDryRunAgreesWhenAPathIsBothCopiedAndLinked(t *testing.T) {
	src := newFilesRepo(t, "*.env\n")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")
	dst := newDestination(t)

	setFileConfig(t, []string{"*.env"}, []string{"app.env"}, nil, false)

	dry, err := runFileCopy(src, dst, copyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	real, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("real run: %v", err)
	}

	if len(dry.Files) != len(real.Files) {
		t.Fatalf("dry run planned %d entries, real run produced %d:\ndry  %+v\nreal %+v",
			len(dry.Files), len(real.Files), dry.Files, real.Files)
	}
	for i := range dry.Files {
		if dry.Files[i].Path != real.Files[i].Path || dry.Files[i].Action != real.Files[i].Action {
			t.Errorf("entry %d: dry %+v, real %+v", i, dry.Files[i], real.Files[i])
		}
	}
	if dry.Summary.Linked != 0 {
		t.Errorf("dry run reported %d links, want 0: the copy claims the path first", dry.Summary.Linked)
	}
	// And the file that landed is the copy, not a symlink back to the source.
	info, err := os.Lstat(filepath.Join(dst, "app.env"))
	if err != nil {
		t.Fatalf("lstat app.env: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("app.env is a symlink; the copy stage should have claimed it")
	}
}

// --force is part of what --dry-run previews: predicting "skipped (exists)" for
// a run that will overwrite is worse than not previewing at all.
func TestDryRunModelsForce(t *testing.T) {
	src := newFilesRepo(t, "*.env\n")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")
	writeFile(t, filepath.Join(src, "dir.env"), "DIR=1")

	dst := newDestination(t)
	writeFile(t, filepath.Join(dst, "app.env"), "EXISTING=1")
	// A directory where a file should go: --force must not replace it.
	if err := os.MkdirAll(filepath.Join(dst, "dir.env"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	setFileConfig(t, []string{"*.env"}, nil, nil, false)

	dry, err := runFileCopy(src, dst, copyOptions{DryRun: true, Force: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	real, err := runFileCopy(src, dst, copyOptions{Force: true})
	if err != nil {
		t.Fatalf("real run: %v", err)
	}

	if len(dry.Files) != len(real.Files) {
		t.Fatalf("dry run planned %d entries, real run produced %d", len(dry.Files), len(real.Files))
	}
	for i := range dry.Files {
		if dry.Files[i].Path != real.Files[i].Path || dry.Files[i].Action != real.Files[i].Action {
			t.Errorf("entry %d: dry %+v, real %+v", i, dry.Files[i], real.Files[i])
		}
	}
	if readFile(t, filepath.Join(dst, "app.env")) != "TOKEN=1" {
		t.Error("--force did not overwrite the existing file")
	}
}

// A directory-only exclude pattern must not apply to a link whose source is a
// regular file — the trailing slash means directories only.
func TestDirectoryOnlyExcludeDoesNotApplyToAFileLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "cache\nbuild\n")
	writeFile(t, filepath.Join(src, "cache"), "a regular file, not a directory")
	writeFile(t, filepath.Join(src, "build", "out.bin"), "binary")

	dst := newDestination(t)
	setFileConfig(t, nil, []string{"cache", "build"}, []string{"build/"}, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Linked != 1 {
		t.Fatalf("linked = %d, want 1 (%+v)", result.Summary.Linked, result.Files)
	}
	if _, err := os.Lstat(filepath.Join(dst, "cache")); err != nil {
		t.Errorf("the file link was excluded by a directory-only pattern: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "build")); !os.IsNotExist(err) {
		t.Error("the directory link was not excluded")
	}
}

// link entries are literal paths: a missing source is a warning, and an
// existing destination is never replaced even with --force.
func TestLinkSemantics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "node_modules/\n")
	writeFile(t, filepath.Join(src, "node_modules", "pkg", "index.js"), "module.exports = {}")

	dst := newDestination(t)
	setFileConfig(t, nil, []string{"node_modules", "never_installed"}, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Linked != 1 {
		t.Fatalf("linked = %d, want 1 (%+v)", result.Summary.Linked, result.Files)
	}
	if result.Summary.Skipped != 1 {
		t.Errorf("skipped = %d, want 1 for the missing source (%+v)", result.Summary.Skipped, result.Files)
	}

	target, err := os.Readlink(filepath.Join(dst, "node_modules"))
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != filepath.Join(src, "node_modules") {
		t.Errorf("target = %q, want %q", target, filepath.Join(src, "node_modules"))
	}

	// Re-running with --force must not swap the existing link out.
	again, err := runFileCopy(src, dst, copyOptions{Force: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if again.Summary.Linked != 0 {
		t.Errorf("linked = %d on re-run, want 0", again.Summary.Linked)
	}
}

// A directory pattern pulls in the whole tree below it.
func TestDirectoryPatternCopiesWholeTree(t *testing.T) {
	src := newFilesRepo(t, "cache/\n")
	writeFile(t, filepath.Join(src, "cache", "a.txt"), "a")
	writeFile(t, filepath.Join(src, "cache", "deep", "b.txt"), "b")

	dst := newDestination(t)
	setFileConfig(t, []string{"cache"}, nil, nil, false)

	if _, err := runFileCopy(src, dst, copyOptions{}); err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	for _, rel := range []string{"cache/a.txt", "cache/deep/b.txt"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s missing: %v", rel, err)
		}
	}
}

// A pattern naming a file inside a wholly-ignored directory must still be
// found: git collapses the directory into one candidate, so the planner has to
// descend into it rather than skip it.
func TestPatternInsideWhollyIgnoredDirectory(t *testing.T) {
	src := newFilesRepo(t, ".claude/\n")
	writeFile(t, filepath.Join(src, ".claude", "settings.local.json"), "{}")
	writeFile(t, filepath.Join(src, ".claude", "cache", "big.bin"), "junk")

	setFileConfig(t, []string{".claude/settings.local.json"}, nil, nil, false)

	files := planPaths(t, src)
	if !equalStrings(files, []string{".claude/settings.local.json"}) {
		t.Errorf("plan = %v, want only .claude/settings.local.json", files)
	}
}

func TestSummariseCountsReflinks(t *testing.T) {
	results := []fileResult{
		{Action: fileActionCopied, Method: string(fileops.MethodReflink)},
		{Action: fileActionCopied, Method: string(fileops.MethodCopy)},
		{Action: fileActionLinked, Method: string(fileops.MethodSymlink)},
		{Action: fileActionSkipped},
		{Action: fileActionFailed},
	}

	got := summarise(results)
	want := copySummary{Copied: 2, Reflinked: 1, Linked: 1, Skipped: 1, Failed: 1}
	if got != want {
		t.Errorf("summarise = %+v, want %+v", got, want)
	}
}

func TestFormatCopySummary(t *testing.T) {
	tests := []struct {
		name string
		in   copySummary
		want string
	}{
		{"nothing", copySummary{}, "Nothing to copy"},
		{"one file", copySummary{Copied: 1}, "Copied 1 file"},
		{"reflinks", copySummary{Copied: 3, Reflinked: 3}, "Copied 3 files (3 reflinked)"},
		{"mixed", copySummary{Copied: 1, Linked: 2, Skipped: 3, Failed: 4}, "Copied 1 file, linked 2, skipped 3, failed 4"},
	}
	for _, tt := range tests {
		if got := formatCopySummary(tt.in); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

// materialiseFiles is what create/checkout/pr/mr call. It must stay silent and
// return nil when there is nothing configured, so unconfigured repos see no
// behaviour change at all.
func TestMaterialiseFilesIsANoOpWhenUnconfigured(t *testing.T) {
	src := newFilesRepo(t, "*.env\n")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")
	dst := newDestination(t)

	setFileConfig(t, nil, nil, nil, false)

	if got := materialiseFiles(repoInfo{Main: src}, dst, false); got != nil {
		t.Errorf("summary = %+v, want nil", got)
	}
	if _, err := os.Lstat(filepath.Join(dst, "app.env")); !os.IsNotExist(err) {
		t.Error("a file was copied without any [files] configuration")
	}
}

func TestMaterialiseFilesRespectsNoCopyFlag(t *testing.T) {
	src := newFilesRepo(t, "*.env\n")
	writeFile(t, filepath.Join(src, "app.env"), "TOKEN=1")
	dst := newDestination(t)

	setFileConfig(t, []string{"*.env"}, nil, nil, false)

	if got := materialiseFiles(repoInfo{Main: src}, dst, true); got != nil {
		t.Errorf("summary = %+v, want nil under --no-copy", got)
	}
	if _, err := os.Lstat(filepath.Join(dst, "app.env")); !os.IsNotExist(err) {
		t.Error("app.env was copied despite --no-copy")
	}
}

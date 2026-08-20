package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests in this file pin the seven security invariants that justify letting
// [files] run without `wt trust`. If one of them stops holding, the trust
// exemption stops being defensible — so each has its own test and each names the
// invariant it guards.

// setFileConfig installs a [files] configuration for the duration of one test,
// bypassing loadWorktreeConfig so the test does not depend on the developer's
// real config. Layer accumulation itself is covered in files_test.go.
func setFileConfig(t *testing.T, copy, link, exclude []string, copyIgnored bool) {
	t.Helper()

	origCopy, origLink, origExclude, origIgnored := filesCopy, filesLink, filesExclude, filesCopyIgnored
	t.Cleanup(func() {
		filesCopy, filesLink, filesExclude, filesCopyIgnored = origCopy, origLink, origExclude, origIgnored
	})

	filesCopy = accumulateFilePatterns(nil, copy, "test")
	filesLink = accumulateFilePatterns(nil, link, "test")
	filesExclude = accumulateFilePatterns(nil, exclude, "test")
	filesCopyIgnored = copyIgnored
}

// newFilesRepo creates a git repository with one commit and an optional
// .gitignore, and returns its path.
func newFilesRepo(t *testing.T, gitignore string) string {
	t.Helper()

	dir := t.TempDir()
	setupTestRepo(t, dir)
	if gitignore != "" {
		writeFile(t, filepath.Join(dir, ".gitignore"), gitignore)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newDestination returns an empty directory standing in for a freshly created
// worktree.
func newDestination(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dst, err)
	}
	return dst
}

func planPaths(t *testing.T, src string) []string {
	t.Helper()

	cfg, err := resolveFileConfig(src)
	if err != nil {
		t.Fatalf("resolveFileConfig: %v", err)
	}
	plan, err := buildCopyPlan(src, cfg)
	if err != nil {
		t.Fatalf("buildCopyPlan: %v", err)
	}
	return plan.files
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// F1 — source patterns resolve strictly inside the main worktree.
func TestSecurityF1SourcePatternsStayInsideWorktree(t *testing.T) {
	tests := []struct {
		name    string
		copy    []string
		link    []string
		exclude []string
	}{
		{name: "copy escapes upward", copy: []string{"../../etc/passwd"}},
		{name: "exclude escapes upward", exclude: []string{"../secrets"}},
		{name: "link is absolute", link: []string{"/etc/passwd"}},
		{name: "copy climbs out mid-path", copy: []string{"config/../../outside.env"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := newFilesRepo(t, "")
			setFileConfig(t, tt.copy, tt.link, tt.exclude, false)

			_, err := resolveFileConfig(src)
			if err == nil {
				t.Fatal("expected the pattern to be rejected, got nil error")
			}
			// The error must name the offending pattern so the user can fix it.
			pattern := firstNonEmpty(tt.copy, tt.link, tt.exclude)
			if !strings.Contains(err.Error(), pattern) {
				t.Errorf("error %q does not name the rejected pattern %q", err, pattern)
			}
		})
	}
}

// A leading "/" is gitignore anchoring, not an absolute path, so it must still
// be accepted for copy and exclude — otherwise "/.env" (top-level .env only)
// would be impossible to express.
func TestSecurityF1AnchoredPatternIsNotAbsolute(t *testing.T) {
	src := newFilesRepo(t, "")
	setFileConfig(t, []string{"/.env"}, nil, []string{"/build"}, false)

	if _, err := resolveFileConfig(src); err != nil {
		t.Fatalf("anchored patterns should be accepted, got: %v", err)
	}
}

func firstNonEmpty(lists ...[]string) string {
	for _, l := range lists {
		if len(l) > 0 {
			return l[0]
		}
	}
	return ""
}

// F2 — destination paths resolve strictly inside the new worktree.
func TestSecurityF2DestinationStaysInsideWorktree(t *testing.T) {
	src := newFilesRepo(t, "")
	writeFile(t, filepath.Join(src, "payload.txt"), "payload")

	dst := newDestination(t)
	outside := filepath.Join(filepath.Dir(dst), "escaped.txt")

	// A relative path that climbs out of the destination must be refused even
	// if it somehow reached copyOne: this is the last line of defence behind
	// the config-time check.
	res := copyOne(src, dst, "../escaped.txt", false)
	if res.Action != fileActionFailed {
		t.Fatalf("action = %q, want %q", res.Action, fileActionFailed)
	}
	if !strings.Contains(res.Reason, "escapes") {
		t.Errorf("reason = %q, want it to mention escaping", res.Reason)
	}
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Errorf("%s was created outside the destination worktree", outside)
	}
}

// The lexical check alone is not enough: a worktree can contain a *tracked*
// symlink, so "cache/secret.txt" stays lexically inside the destination and
// still writes to wherever "cache" points.
func TestSecurityF2SymlinkedDestinationParentIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "cache/\n")
	writeFile(t, filepath.Join(src, "cache", "secret.txt"), "secret")

	dst := newDestination(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dst, "cache")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	setFileConfig(t, []string{"cache/secret.txt"}, nil, nil, false)

	// --dry-run must predict the refusal rather than promise a copy.
	preview, err := runFileCopy(src, dst, copyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if preview.Summary.Copied != 0 {
		t.Errorf("dry run reported %d copies, want 0 (%+v)", preview.Summary.Copied, preview.Files)
	}

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Copied != 0 {
		t.Errorf("copied = %d, want 0 (%+v)", result.Summary.Copied, result.Files)
	}
	if _, err := os.Lstat(filepath.Join(outside, "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("wrote through the symlinked parent into %s", outside)
	}

	// --force must not talk its way through either.
	if _, err := runFileCopy(src, dst, copyOptions{Force: true}); err != nil {
		t.Fatalf("forced run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("--force wrote through the symlinked parent into %s", outside)
	}
}

// A directory in the plan gets MkdirAll'd and Chmod'd, and both follow a
// symlink standing where the directory should be — so the leaf needs the check
// that a file leaf does not.
func TestSecurityF7SymlinkedDestinationDirectoryIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "cache/\n")
	writeFile(t, filepath.Join(src, "cache", "a.txt"), "a")
	if err := os.Chmod(filepath.Join(src, "cache"), 0o700); err != nil {
		t.Fatalf("chmod source: %v", err)
	}

	dst := newDestination(t)
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o755); err != nil {
		t.Fatalf("chmod outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dst, "cache")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	setFileConfig(t, []string{"cache/"}, nil, nil, false)

	if _, err := runFileCopy(src, dst, copyOptions{}); err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(outside, "a.txt")); !os.IsNotExist(err) {
		t.Errorf("wrote into %s through the symlinked directory", outside)
	}
	info, err := os.Stat(outside)
	if err != nil {
		t.Fatalf("stat outside: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("mode of %s = %v, want 0755 unchanged", outside, info.Mode().Perm())
	}
}

// The same guard has to hold for link, which builds its paths independently of
// the copy planner.
func TestSecurityF2SymlinkedLinkParentIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "vendor/\n")
	writeFile(t, filepath.Join(src, "vendor", "dep", "index.js"), "{}")

	dst := newDestination(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dst, "vendor")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	setFileConfig(t, nil, []string{"vendor/dep"}, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Linked != 0 {
		t.Errorf("linked = %d, want 0 (%+v)", result.Summary.Linked, result.Files)
	}
	if _, err := os.Lstat(filepath.Join(outside, "dep")); !os.IsNotExist(err) {
		t.Fatalf("linked through the symlinked parent into %s", outside)
	}
}

// .worktreeinclude is committed, so a hostile repo could ship it as a symlink
// to a file outside the worktree and have wt read it — and echo its lines back
// through `wt info`.
func TestSecurityF7WorktreeIncludeSymlinkIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	isolateFileConfig(t)

	outside := filepath.Join(t.TempDir(), "private.txt")
	writeFile(t, outside, "id_rsa\n")

	src := newFilesRepo(t, "")
	if err := os.Symlink(outside, filepath.Join(src, worktreeIncludeFile)); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cfg, err := resolveFileConfig(src)
	if err == nil {
		t.Fatalf("expected an error, got patterns %v", patternStrings(cfg.Copy))
	}
	if !strings.Contains(err.Error(), "regular file") {
		t.Errorf("error = %q, want it to name the refusal", err)
	}
	if contains(patternStrings(cfg.Copy), "id_rsa") {
		t.Error("patterns were read from outside the worktree")
	}
}

func TestSecurityF2WithinRoot(t *testing.T) {
	root := t.TempDir()

	tests := []struct {
		path string
		want bool
	}{
		{filepath.Join(root, "a"), true},
		{filepath.Join(root, "a", "b", "c"), true},
		{root, false},
		{filepath.Dir(root), false},
		{filepath.Join(root, "..", "sibling"), false},
		{filepath.Join(root, "a", "..", "..", "sibling"), false},
	}

	for _, tt := range tests {
		if got := withinRoot(root, tt.path); got != tt.want {
			t.Errorf("withinRoot(%q, %q) = %v, want %v", root, tt.path, got, tt.want)
		}
	}
}

// F3 — ".." path segments are rejected outright at config-resolution time.
func TestSecurityF3DotDotSegmentsRejected(t *testing.T) {
	src := newFilesRepo(t, "")

	for _, pattern := range []string{"..", "../x", "a/../../b", "a/..", "!../x"} {
		t.Run(pattern, func(t *testing.T) {
			setFileConfig(t, []string{pattern}, nil, nil, false)
			if _, err := resolveFileConfig(src); err == nil {
				t.Fatalf("pattern %q with a %q segment was accepted", pattern, "..")
			}
		})
	}
}

// F4 — symlinks are copied as symlinks, never dereferenced, even when the
// target escapes the main worktree.
func TestSecurityF4SymlinksAreNeverDereferenced(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "*.link\n")
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	writeFile(t, outsideFile, "top secret")

	escaping := filepath.Join(src, "escape.link")
	if err := os.Symlink(outsideFile, escaping); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	dst := newDestination(t)
	setFileConfig(t, []string{"*.link"}, nil, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Copied != 1 {
		t.Fatalf("copied = %d, want 1 (%+v)", result.Summary.Copied, result.Files)
	}

	copied := filepath.Join(dst, "escape.link")
	info, err := os.Lstat(copied)
	if err != nil {
		t.Fatalf("lstat %s: %v", copied, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("escaping symlink was dereferenced into a regular file")
	}
	target, err := os.Readlink(copied)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != outsideFile {
		t.Errorf("target = %q, want %q (copied verbatim)", target, outsideFile)
	}
}

// A symlinked directory must not be walked: descending it would read files
// outside the worktree (F4 together with F7).
func TestSecurityF4SymlinkedDirectoryIsNotWalked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	// "outside" without a trailing slash: the entry is a symlink, not a
	// directory, so a directory-only pattern would not match it.
	src := newFilesRepo(t, "outside\n")
	outsideDir := t.TempDir()
	writeFile(t, filepath.Join(outsideDir, "secret.txt"), "top secret")

	if err := os.Symlink(outsideDir, filepath.Join(src, "outside")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	setFileConfig(t, nil, nil, nil, true)
	files := planPaths(t, src)

	if contains(files, "outside/secret.txt") {
		t.Errorf("walked through a symlinked directory: %v", files)
	}
	if !contains(files, "outside") {
		t.Errorf("the symlink itself should be copied, got %v", files)
	}
}

// F5 — files tracked by git are never copied.
func TestSecurityF5TrackedFilesAreNeverCopied(t *testing.T) {
	// A .gitignore matching everything, plus a force-added tracked file: the
	// pattern says "copy me" but git reports the file as tracked, so it must
	// stay out of the candidate set.
	src := newFilesRepo(t, "*.txt\n")
	writeFile(t, filepath.Join(src, "tracked.txt"), "committed content")
	writeFile(t, filepath.Join(src, "ignored.txt"), "ignored content")
	runGitCommand(t, src, "add", "-f", "tracked.txt")
	runGitCommand(t, src, "commit", "-m", "add tracked file")

	// Diverge the working copy: if a tracked file were ever copied, this is the
	// content that would silently overwrite the new worktree's checkout.
	writeFile(t, filepath.Join(src, "tracked.txt"), "uncommitted working state")

	setFileConfig(t, []string{"*.txt"}, nil, nil, true)
	files := planPaths(t, src)

	if contains(files, "tracked.txt") {
		t.Errorf("tracked file selected for copy: %v", files)
	}
	if !contains(files, "ignored.txt") {
		t.Errorf("ignored file missing from plan: %v", files)
	}
}

// link names its paths literally instead of drawing them from the ignored
// candidate list, so it needs its own tracked-path guard to keep F5.
func TestSecurityF5TrackedPathsAreNeverLinked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "")
	writeFile(t, filepath.Join(src, "vendor", "dep.js"), "{}")
	runGitCommand(t, src, "add", "vendor")
	runGitCommand(t, src, "commit", "-m", "track vendor")

	dst := newDestination(t)
	setFileConfig(t, nil, []string{"vendor"}, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Linked != 0 {
		t.Fatalf("linked = %d, want 0 (%+v)", result.Summary.Linked, result.Files)
	}
	if result.Files[0].Reason != "tracked by git" {
		t.Errorf("reason = %q, want %q", result.Files[0].Reason, "tracked by git")
	}
	if _, err := os.Lstat(filepath.Join(dst, "vendor")); !os.IsNotExist(err) {
		t.Error("a tracked path was linked into the destination")
	}
}

// F6 — an existing destination path is never overwritten without --force.
func TestSecurityF6ExistingDestinationIsNotOverwritten(t *testing.T) {
	src := newFilesRepo(t, ".env\n")
	writeFile(t, filepath.Join(src, ".env"), "SOURCE=1")

	dst := newDestination(t)
	writeFile(t, filepath.Join(dst, ".env"), "DESTINATION=1")

	setFileConfig(t, []string{".env"}, nil, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Skipped != 1 || result.Summary.Copied != 0 {
		t.Fatalf("summary = %+v, want 1 skipped and 0 copied", result.Summary)
	}
	if got := readFile(t, filepath.Join(dst, ".env")); got != "DESTINATION=1" {
		t.Errorf("destination content = %q, want it untouched", got)
	}

	// --force is the documented way to opt in to overwriting.
	forced, err := runFileCopy(src, dst, copyOptions{Force: true})
	if err != nil {
		t.Fatalf("runFileCopy --force: %v", err)
	}
	if forced.Summary.Copied != 1 {
		t.Fatalf("summary = %+v, want 1 copied under --force", forced.Summary)
	}
	if got := readFile(t, filepath.Join(dst, ".env")); got != "SOURCE=1" {
		t.Errorf("destination content = %q, want %q", got, "SOURCE=1")
	}
}

// --force must still refuse to replace a directory with a file.
func TestSecurityF6ForceDoesNotReplaceDirectory(t *testing.T) {
	src := newFilesRepo(t, "state\n")
	writeFile(t, filepath.Join(src, "state"), "a file in the source")

	dst := newDestination(t)
	if err := os.MkdirAll(filepath.Join(dst, "state", "keep"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	setFileConfig(t, []string{"state"}, nil, nil, false)

	result, err := runFileCopy(src, dst, copyOptions{Force: true})
	if err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}
	if result.Summary.Failed != 1 {
		t.Fatalf("summary = %+v, want 1 failed", result.Summary)
	}
	if info, err := os.Lstat(filepath.Join(dst, "state", "keep")); err != nil || !info.IsDir() {
		t.Error("the destination directory and its contents should survive --force")
	}
}

// F7 — nothing outside the main worktree, the new worktree and TempDir is read
// or written.
func TestSecurityF7NothingOutsideTheWorktreesIsTouched(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevation on Windows")
	}

	src := newFilesRepo(t, "outside\nescape.link\n")
	outsideDir := t.TempDir()
	writeFile(t, filepath.Join(outsideDir, "secret.txt"), "top secret")

	if err := os.Symlink(outsideDir, filepath.Join(src, "outside")); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}
	if err := os.Symlink(filepath.Join(outsideDir, "secret.txt"), filepath.Join(src, "escape.link")); err != nil {
		t.Fatalf("symlink file: %v", err)
	}

	before := listTree(t, outsideDir)

	dst := newDestination(t)
	setFileConfig(t, nil, nil, nil, true)
	if _, err := runFileCopy(src, dst, copyOptions{Force: true}); err != nil {
		t.Fatalf("runFileCopy: %v", err)
	}

	if after := listTree(t, outsideDir); !equalStrings(before, after) {
		t.Errorf("the outside directory changed: before %v, after %v", before, after)
	}
	if got := readFile(t, filepath.Join(outsideDir, "secret.txt")); got != "top secret" {
		t.Errorf("outside file content = %q, want it untouched", got)
	}
	// The escaping symlinks are reproduced, but their targets are never
	// followed: the destination gets a symlink, not a copied-out tree.
	info, err := os.Lstat(filepath.Join(dst, "outside"))
	if err != nil {
		t.Fatalf("lstat destination symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("content from outside the worktree was materialised into the destination")
	}
}

// The planner must refuse a path that resolves outside the source even when it
// arrives through the walk rather than through configuration.
func TestSecurityF7PlannerSkipsPathsOutsideSource(t *testing.T) {
	src := newFilesRepo(t, "")
	p := &planner{src: src, cfg: fileConfig{}, worktreePaths: map[string]bool{}}

	if !p.skip("../outside", "outside") {
		t.Error("planner walked a path outside the source worktree")
	}
	if !p.skip(".git", ".git") {
		t.Error("planner did not skip .git")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// listTree returns every path under root, relative and sorted, for comparing a
// directory before and after an operation.
func listTree(t *testing.T, root string) []string {
	t.Helper()

	var paths []string
	err := filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return paths
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

package fileops

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
	// WriteFile applies the umask, which would make a 0o755 fixture arrive as
	// 0o755 &^ umask and quietly weaken the mode-preservation assertions.
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("failed to chmod %s: %v", path, err)
	}
}

func TestCopyFileCopiesContentAndMode(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	writeFile(t, src, "hello worktree", 0o640)

	method, err := CopyFile(src, dst)
	if err != nil {
		t.Fatalf("CopyFile returned error: %v", err)
	}
	if method != MethodReflink && method != MethodCopy {
		t.Errorf("CopyFile reported method %q, want reflink or copy", method)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if string(got) != "hello worktree" {
		t.Errorf("destination content = %q, want %q", got, "hello worktree")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("failed to stat destination: %v", err)
		}
		if info.Mode().Perm() != 0o640 {
			t.Errorf("destination mode = %v, want %v", info.Mode().Perm(), os.FileMode(0o640))
		}
	}
}

func TestCopyFilePreservesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no executable bit on Windows")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "run.sh")
	dst := filepath.Join(dir, "run-copy.sh")
	writeFile(t, src, "#!/bin/sh\n", 0o755)

	if _, err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile returned error: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("failed to stat destination: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("destination mode = %v, want %v", info.Mode().Perm(), os.FileMode(0o755))
	}
}

func TestCopyFileZeroByteFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "empty")
	dst := filepath.Join(dir, "empty-copy")
	writeFile(t, src, "", 0o644)

	if _, err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile returned error: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("failed to stat destination: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("destination size = %d, want 0", info.Size())
	}
}

func TestCopyFileRefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	writeFile(t, src, "new", 0o644)
	writeFile(t, dst, "original", 0o644)

	if _, err := CopyFile(src, dst); !errors.Is(err, os.ErrExist) {
		t.Fatalf("CopyFile over an existing file returned %v, want os.ErrExist", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("destination content = %q, want it left untouched", got)
	}
}

func TestCopyFilePreservesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link")
	dst := filepath.Join(dir, "link-copy")
	writeFile(t, target, "payload", 0o644)
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	method, err := CopyFile(link, dst)
	if err != nil {
		t.Fatalf("CopyFile returned error: %v", err)
	}
	if method != MethodSymlink {
		t.Errorf("CopyFile reported method %q, want %q", method, MethodSymlink)
	}
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("failed to lstat destination: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("destination is not a symlink; the source was dereferenced")
	}
	got, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("failed to readlink destination: %v", err)
	}
	if got != target {
		t.Errorf("symlink target = %q, want %q", got, target)
	}
}

func TestCopyFilePreservesBrokenSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "broken")
	dst := filepath.Join(dir, "broken-copy")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	method, err := CopyFile(link, dst)
	if err != nil {
		t.Fatalf("CopyFile on a broken symlink returned error: %v", err)
	}
	if method != MethodSymlink {
		t.Errorf("CopyFile reported method %q, want %q", method, MethodSymlink)
	}
	if _, err := os.Lstat(dst); err != nil {
		t.Fatalf("broken symlink was not recreated: %v", err)
	}
}

func TestCopyFileMissingSource(t *testing.T) {
	dir := t.TempDir()
	_, err := CopyFile(filepath.Join(dir, "nope"), filepath.Join(dir, "dst"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CopyFile with a missing source returned %v, want os.ErrNotExist", err)
	}
}

func TestCopyFileUnreadableSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0 does not deny reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "secret")
	dst := filepath.Join(dir, "secret-copy")
	writeFile(t, src, "shh", 0o000)

	if _, err := CopyFile(src, dst); err == nil {
		t.Fatal("CopyFile on an unreadable source succeeded, want a permission error")
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Error("a failed copy left a destination file behind")
	}
}

func TestReflinkOrUnsupported(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	writeFile(t, src, strings.Repeat("x", 4096), 0o644)

	err := Reflink(src, dst)
	if errors.Is(err, ErrUnsupported) {
		t.Skipf("filesystem under %s does not support reflink", dir)
	}
	if err != nil {
		t.Fatalf("Reflink returned error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if len(got) != 4096 {
		t.Errorf("destination size = %d, want 4096", len(got))
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("failed to stat destination: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o644 {
		t.Errorf("destination mode = %v, want %v", info.Mode().Perm(), os.FileMode(0o644))
	}
}

func TestReflinkRefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	dst := filepath.Join(dir, "dst.bin")
	writeFile(t, src, "new", 0o644)
	writeFile(t, dst, "original", 0o644)

	err := Reflink(src, dst)
	if errors.Is(err, ErrUnsupported) {
		t.Skipf("filesystem under %s does not support reflink", dir)
	}
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("Reflink over an existing file returned %v, want os.ErrExist", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if string(got) != "original" {
		t.Errorf("destination content = %q, want it left untouched", got)
	}
}

func TestReflinkFallbackLeavesNoStub(t *testing.T) {
	// Whatever Reflink does, a failure must not leave a partial destination
	// behind: the buffered fallback opens with O_EXCL and would trip over it.
	dir := t.TempDir()
	src := filepath.Join(dir, "adir")
	dst := filepath.Join(dir, "dst")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}

	if err := Reflink(src, dst); err == nil {
		t.Fatal("Reflink of a directory succeeded, want an error")
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Error("a failed Reflink left a destination behind")
	}
}

func TestMkdirAllFromCopiesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes are not meaningful on Windows")
	}
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.Mkdir(srcDir, 0o700); err != nil {
		t.Fatalf("failed to create source directory: %v", err)
	}
	if err := os.Chmod(srcDir, 0o700); err != nil {
		t.Fatalf("failed to chmod source directory: %v", err)
	}

	dstDir := filepath.Join(dir, "out", "nested", "src")
	if err := MkdirAllFrom(dstDir, srcDir); err != nil {
		t.Fatalf("MkdirAllFrom returned error: %v", err)
	}
	info, err := os.Stat(dstDir)
	if err != nil {
		t.Fatalf("failed to stat created directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("created directory mode = %v, want %v", info.Mode().Perm(), os.FileMode(0o700))
	}

	// Invented parents get 0755 because there is no source to copy from.
	parent, err := os.Stat(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatalf("failed to stat invented parent: %v", err)
	}
	if parent.Mode().Perm() != 0o755 {
		t.Errorf("invented parent mode = %v, want %v", parent.Mode().Perm(), os.FileMode(0o755))
	}
}

// Copying the contents of a directory is not licence to re-permission the
// destination: a worktree's 0700 cache/ must not be widened to the source's
// 0755 just because a copy passed through it.
func TestMkdirAllFromLeavesAnExistingDirectoryAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory modes are not meaningful on Windows")
	}
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "src")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatalf("failed to create source directory: %v", err)
	}
	if err := os.Chmod(srcDir, 0o755); err != nil {
		t.Fatalf("failed to chmod source directory: %v", err)
	}

	dstDir := filepath.Join(dir, "dst")
	if err := os.Mkdir(dstDir, 0o700); err != nil {
		t.Fatalf("failed to create destination directory: %v", err)
	}
	if err := os.Chmod(dstDir, 0o700); err != nil {
		t.Fatalf("failed to chmod destination directory: %v", err)
	}

	if err := MkdirAllFrom(dstDir, srcDir); err != nil {
		t.Fatalf("MkdirAllFrom returned error: %v", err)
	}
	info, err := os.Stat(dstDir)
	if err != nil {
		t.Fatalf("failed to stat destination directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("existing directory mode = %v, want it left at %v", info.Mode().Perm(), os.FileMode(0o700))
	}
}

func TestEnsureParentCreatesMissingDirectories(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a", "b", "c.txt")
	if err := EnsureParent(target); err != nil {
		t.Fatalf("EnsureParent returned error: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "a", "b"))
	if err != nil {
		t.Fatalf("parent directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("EnsureParent created something that is not a directory")
	}
}

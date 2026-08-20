// Package fileops materialises files from one directory tree into another.
//
// The interesting part is Reflink: on APFS, Btrfs, XFS and ZFS the filesystem
// can clone a file's extents instead of copying its bytes, which is what makes
// seeding a worktree with a multi-gigabyte node_modules or target/ directory
// cheap enough to do on every `wt create`. Everywhere else the buffered
// fallback is used and the result is identical, just slower.
package fileops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Method records how a single file was materialised.
type Method string

const (
	// MethodReflink means the filesystem cloned the file's extents.
	MethodReflink Method = "reflink"
	// MethodCopy means the bytes were read and written.
	MethodCopy Method = "copy"
	// MethodSymlink means a symlink was recreated rather than dereferenced.
	MethodSymlink Method = "symlink"
)

// ErrUnsupported reports that the platform or filesystem cannot reflink.
var ErrUnsupported = errors.New("reflink not supported")

// CopyFile copies src to dst, attempting a reflink first and falling back to
// a buffered copy. Returns the method actually used. File mode is preserved.
//
// A symlink source is recreated as a symlink and never dereferenced, so a link
// pointing outside the tree being copied stays a dangling name rather than
// becoming a copy of whatever it aimed at.
//
// dst is created exclusively: if it already exists, CopyFile fails with an
// os.ErrExist error and does not touch it. That is the whole no-clobber
// guarantee, enforced by the open(2) flags rather than by a stat that another
// process could invalidate in between.
func CopyFile(src, dst string) (Method, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return "", err
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return "", err
		}
		if err := os.Symlink(target, dst); err != nil {
			return "", err
		}
		return MethodSymlink, nil

	case info.Mode().IsRegular():
		// Nothing else is worth attempting for a device node, socket or FIFO,
		// and copying one into a worktree is never what the user meant.
	default:
		return "", fmt.Errorf("unsupported file type %s: %s", info.Mode().Type(), src)
	}

	switch err := Reflink(src, dst); {
	case err == nil:
		return MethodReflink, nil
	case errors.Is(err, ErrUnsupported):
		// Fall through to the buffered copy.
	default:
		// An existing destination or a permission problem is a real error and
		// must not be retried as a plain copy: the retry would report a
		// different (and less accurate) reason for the same failure.
		return "", err
	}

	if err := bufferedCopy(src, dst, info.Mode().Perm()); err != nil {
		return "", err
	}
	return MethodCopy, nil
}

// bufferedCopy reads src and writes it to a freshly created dst.
func bufferedCopy(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}

	if _, err := copyRange(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}

	// An existing umask narrows the mode passed to OpenFile, so set it
	// explicitly to keep an executable source executable.
	if err := os.Chmod(dst, perm); err != nil {
		return err
	}
	return nil
}

// MkdirAllFrom creates dir and any missing parents, giving dir the mode of the
// directory it is being copied from. Parents that have to be invented get
// 0755, since there is nothing to copy their mode from.
func MkdirAllFrom(dir string, srcDir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if srcDir == "" {
		return nil
	}
	info, err := os.Stat(srcDir)
	if err != nil {
		// The source mode is a nicety; a missing source is the caller's
		// problem to report, not a reason to fail the mkdir.
		return nil //nolint:nilerr // best-effort mode propagation
	}
	return os.Chmod(dir, info.Mode().Perm())
}

// EnsureParent creates the parent directory of path if it is missing.
func EnsureParent(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o755)
}

// copyRange copies in to out, preferring an in-kernel copy where the platform
// offers one (see copy_range_linux.go).
func copyRange(out *os.File, in *os.File) (int64, error) {
	if n, err, handled := kernelCopy(out, in); handled {
		return n, err
	}
	return io.Copy(out, in)
}

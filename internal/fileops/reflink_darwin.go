package fileops

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Reflink attempts a copy-on-write clone using clonefile(2), which APFS
// implements. Returns ErrUnsupported if the filesystem cannot do it.
//
// clonefile fails with EEXIST when the destination exists, so the no-clobber
// guarantee comes from the syscall itself. CLONE_NOFOLLOW keeps a symlinked
// source a symlink rather than cloning its target.
func Reflink(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return ErrUnsupported
	}

	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		if isUnsupportedClone(err) {
			return ErrUnsupported
		}
		return err
	}
	return nil
}

// isUnsupportedClone reports whether the error means "this filesystem cannot
// clone" rather than "this particular clone failed".
func isUnsupportedClone(err error) bool {
	for _, e := range []error{
		unix.ENOTSUP, // filesystem is not APFS
		unix.EXDEV,   // source and destination on different filesystems
		unix.EINVAL,
		unix.ENOTTY,
		unix.EPERM,
		unix.EISDIR,
	} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

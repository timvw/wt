package fileops

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Reflink attempts a copy-on-write clone using the FICLONE ioctl, which Btrfs,
// XFS (with reflink=1) and some other filesystems implement. Returns
// ErrUnsupported if the platform or filesystem cannot do it.
//
// dst is created with O_EXCL, so an existing destination fails with an
// os.ErrExist error rather than being cloned over.
func Reflink(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return ErrUnsupported
	}

	out, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		// Nothing was created, so there is nothing to clean up. In particular
		// an EEXIST must propagate untouched: the caller relies on it to know
		// the destination was left alone.
		return err
	}

	cloneErr := unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
	closeErr := out.Close()

	if cloneErr != nil {
		// The file was created by this call and holds nothing useful, so
		// remove it before the caller falls back to a buffered copy — which
		// opens with O_EXCL too and would otherwise collide with our own stub.
		_ = os.Remove(dst)
		if isUnsupportedClone(cloneErr) {
			return ErrUnsupported
		}
		return cloneErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return closeErr
	}

	// A umask can narrow the mode O_CREAT asked for; restore it so an
	// executable source stays executable.
	return os.Chmod(dst, info.Mode().Perm())
}

// isUnsupportedClone reports whether the error means "this filesystem cannot
// reflink" rather than "this particular clone failed".
func isUnsupportedClone(err error) bool {
	for _, e := range []error{
		unix.EOPNOTSUPP, // filesystem has no clone support
		unix.EINVAL,     // e.g. cloning across incompatible mounts
		unix.EXDEV,      // source and destination on different filesystems
		unix.ENOTTY,     // ioctl not understood at all
		unix.ENOSYS,     // kernel too old
		unix.EPERM,      // e.g. immutable/append-only destination
		unix.ETXTBSY,    // busy executable
		unix.EISDIR,
		unix.EBADF,
	} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

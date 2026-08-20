//go:build !linux && !darwin

package fileops

// Reflink always reports ErrUnsupported on platforms with no copy-on-write
// clone syscall, so callers fall back to a buffered copy.
//
// Windows ReFS can block-clone via FSCTL_DUPLICATE_EXTENTS_TO_FILE; wiring
// that up is deliberately left for later, since NTFS — what almost every
// Windows checkout actually sits on — cannot do it either way.
func Reflink(_, _ string) error {
	return ErrUnsupported
}

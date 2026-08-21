//go:build unix

package fileops

import (
	"golang.org/x/sys/unix"
)

// SameFilesystem reports whether a and b live on the same filesystem.
//
// A reflink can never span filesystems, so this answers "could a clone work
// here" without writing anything — which is what lets --dry-run report the
// method it would use without touching the user's worktree.
func SameFilesystem(a, b string) bool {
	var sa, sb unix.Stat_t
	if err := unix.Stat(a, &sa); err != nil {
		return false
	}
	if err := unix.Stat(b, &sb); err != nil {
		return false
	}
	return sa.Dev == sb.Dev
}

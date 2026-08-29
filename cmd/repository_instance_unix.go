//go:build unix

package cmd

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// repositoryInstanceID identifies the filesystem object at path. Device and
// inode survive a rename and are shared by every linked worktree through their
// common git directory, but change when that directory is removed and replaced.
func repositoryInstanceID(path string) (string, error) {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return "", err
	}
	return fmt.Sprintf("unix:%d:%d", uint64(stat.Dev), uint64(stat.Ino)), nil
}

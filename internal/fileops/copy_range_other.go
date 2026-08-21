//go:build !linux

package fileops

import "os"

// kernelCopy has no in-kernel copy path outside Linux, so it always defers to
// io.Copy by reporting handled=false.
func kernelCopy(_ *os.File, _ *os.File) (int64, error, bool) {
	return 0, nil, false
}

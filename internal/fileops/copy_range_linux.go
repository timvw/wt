package fileops

import (
	"os"

	"golang.org/x/sys/unix"
)

// kernelCopy uses copy_file_range(2), which moves the bytes without a trip
// through user space. It is a worthwhile win on filesystems that cannot
// reflink, and a no-op decision everywhere it is unavailable: handled=false
// sends the caller to io.Copy with nothing written.
func kernelCopy(out *os.File, in *os.File) (int64, error, bool) {
	info, err := in.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, nil, false
	}

	remaining := info.Size()
	var written int64
	for remaining > 0 {
		n, err := unix.CopyFileRange(int(in.Fd()), nil, int(out.Fd()), nil, int(remaining), 0)
		if err != nil {
			if written == 0 {
				// Nothing was written, so io.Copy can still start from the
				// beginning. Anything else (a short copy that then failed)
				// has to be reported, since the file is already partial.
				return 0, nil, false
			}
			return written, err, true
		}
		if n == 0 {
			// The source is shorter than its stat said; treat it as done.
			break
		}
		written += int64(n)
		remaining -= int64(n)
	}
	return written, nil, true
}

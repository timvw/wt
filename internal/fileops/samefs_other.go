//go:build !unix

package fileops

// SameFilesystem is unanswerable without platform-specific stat data, and the
// platforms that reach this file have no Reflink implementation anyway, so the
// conservative answer is the correct one.
func SameFilesystem(_, _ string) bool {
	return false
}

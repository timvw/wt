//go:build !windows

package main

import (
	"os"

	"github.com/creack/pty"
)

// mkPty creates a pseudo-terminal pair (pty, tty)
// This is a simple wrapper around creack/pty for cross-platform compatibility
func mkPty() (*os.File, *os.File, error) {
	return pty.Open()
}

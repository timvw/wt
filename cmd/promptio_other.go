//go:build !windows

package cmd

// enableVirtualTerminalOnStderr is a no-op away from Windows, where terminals
// interpret VT escape sequences without being asked.
func enableVirtualTerminalOnStderr() {}

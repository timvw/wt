package cmd

import (
	"sync"

	"golang.org/x/sys/windows"
)

var vtOnce sync.Once

// vtMode is what stderr needs for escape sequences to be interpreted.
// ENABLE_VIRTUAL_TERMINAL_PROCESSING is documented as requiring
// ENABLE_PROCESSED_OUTPUT, which is on by default but need not have stayed
// that way if a parent process cleared it, so both are set together.
const vtMode = windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING

// enableVirtualTerminalOnStderr puts the stderr console into a mode that
// interprets VT escape sequences, so the raw sequences promptui writes there
// render as a redrawing menu rather than as literal control characters.
//
// Windows Terminal and ConPTY already have this on, but a plain conhost window
// — an unmodernised PowerShell or cmd host — does not, and without it the
// prompt is unreadable. Enabling it is the supported way to opt in.
//
// Best effort by design: the handle may be a pipe or a file rather than a
// console (both are normal for wt, whose integrations redirect streams), in
// which case there is no mode to set and nothing to do. Failures leave the
// mode untouched, which is exactly the state we would be in anyway.
func enableVirtualTerminalOnStderr() {
	vtOnce.Do(func() {
		h, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
		if err != nil || h == windows.InvalidHandle {
			return
		}
		var mode uint32
		if err := windows.GetConsoleMode(h, &mode); err != nil {
			return
		}
		if mode&vtMode == vtMode {
			return
		}
		_ = windows.SetConsoleMode(h, mode|vtMode)
	})
}

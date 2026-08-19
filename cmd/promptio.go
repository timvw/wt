package cmd

import (
	"io"
	"os"
)

// nopWriteCloser adapts a Writer to the io.WriteCloser that promptui's Stdout
// fields expect, without handing the prompt the ability to close the stream.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// promptOutput is where interactive prompts render.
//
// Deliberately not stdout. The shell integration captures stdout to find the
// "wt navigating to:" marker, and on Git Bash — which ships no script(1) — that
// capture is a plain file redirect rather than a PTY, so a menu written to
// stdout lands in the temp file and never reaches the terminal. The prompt is
// live and accepts keystrokes, but the user cannot see what they are selecting
// (issue #124).
//
// Every integration leaves stderr connected to the terminal, so prompts render
// there and stdout stays reserved for the marker and result output.
//
// Deliberately os.Stderr and not readline.Stderr, which is the ANSI-adapted
// writer promptui would otherwise have defaulted to on Windows. That adapter
// translates escape sequences into console API calls, but it issues every one
// of them against the *stdout* handle (readline's package-level `stdout`), not
// against the writer it wraps. Under any wt integration stdout is redirected —
// to a file in the Git Bash fallback, to a variable in the PowerShell wrapper —
// so those calls target something that is not a console, fail, and the adapter
// swallows the sequence rather than emitting it. The menu would print but never
// erase or reposition, so each redraw would stack below the last. Writing raw
// VT to stderr avoids that; enableVirtualTerminalOnStderr makes sure the console
// is in a mode that interprets it.
func promptOutput() io.WriteCloser {
	enableVirtualTerminalOnStderr()
	return nopWriteCloser{os.Stderr}
}

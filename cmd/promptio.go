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
func promptOutput() io.WriteCloser { return nopWriteCloser{os.Stderr} }

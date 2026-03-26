package main

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// ANSI escape code constants.
const (
	ansiReset   = "\033[0m"
	ansiBold    = "1"
	ansiDim     = "2"
	ansiRed     = "31"
	ansiGreen   = "32"
	ansiYellow  = "33"
	ansiCyan    = "36"
	ansiCyanRaw = "36" // for combining with other codes
)

// colorize wraps s in ANSI escape codes. The code parameter can be a single
// code (e.g. "31" for red) or combined codes separated by semicolons
// (e.g. "1;36" for bold cyan).
func colorize(s string, code string) string {
	return fmt.Sprintf("\033[%sm%s%s", code, s, ansiReset)
}

// isColorEnabled returns true if color output should be used.
// Color is disabled when:
//   - the NO_COLOR environment variable is set (any value, per https://no-color.org/)
//   - stdout is not a terminal (i.e., output is piped)
func isColorEnabled() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

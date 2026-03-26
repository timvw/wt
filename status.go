package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

type worktreeStatus struct {
	Path        string `json:"path"`
	Branch      string `json:"branch"`
	HEAD        string `json:"head,omitempty"`
	Dirty       bool   `json:"dirty"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
	Current     bool   `json:"current"`
	HasUpstream bool   `json:"has_upstream"`
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status dashboard of all worktrees",
	Long: `Show a dashboard of all worktrees with their status.

For each worktree, shows: path, branch, dirty/clean state, and
ahead/behind counts relative to upstream.

Use --format json for machine-readable output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := getWorktreeListPorcelain()
		if err != nil {
			return fmt.Errorf("failed to list worktrees: %w", err)
		}

		cwd, err := os.Getwd()
		if err != nil {
			cwd = ""
		}

		var statuses []worktreeStatus
		for _, e := range entries {
			st := worktreeStatus{
				Path:    e.Path,
				Branch:  e.Branch,
				HEAD:    e.HEAD,
				Current: e.Path == cwd,
			}

			// Determine dirty state
			porcelainOut, gitErr := gitStatusPorcelain(e.Path)
			if gitErr == nil {
				st.Dirty = isDirtyStatus(porcelainOut)
			}

			// Determine ahead/behind
			revListOut, revErr := gitRevListAheadBehind(e.Path)
			if revErr == nil {
				ahead, behind, parseErr := parseAheadBehind(revListOut)
				if parseErr == nil {
					st.Ahead = ahead
					st.Behind = behind
					st.HasUpstream = true
				}
			}

			statuses = append(statuses, st)
		}

		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{"worktrees": statuses})
		}

		useColor := isColorEnabled()
		for _, st := range statuses {
			fmt.Println(formatStatusLineColor(st, useColor))
		}
		return nil
	},
}

// parseAheadBehind parses the output of git rev-list --left-right --count HEAD...@{upstream}.
// The output format is "ahead\tbehind\n".
func parseAheadBehind(output string) (int, int, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return 0, 0, fmt.Errorf("empty rev-list output")
	}

	parts := strings.Split(trimmed, "\t")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("unexpected rev-list format: %q", trimmed)
	}

	ahead, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid ahead count %q: %w", parts[0], err)
	}

	behind, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid behind count %q: %w", parts[1], err)
	}

	return ahead, behind, nil
}

// isDirtyStatus returns true if the porcelain output indicates uncommitted changes.
func isDirtyStatus(porcelainOutput string) bool {
	return strings.TrimSpace(porcelainOutput) != ""
}

// formatStatusLine formats a single worktree status entry as a human-readable line (no color).
func formatStatusLine(st worktreeStatus) string {
	return formatStatusLineColor(st, false)
}

// formatStatusLineColor formats a single worktree status entry as a human-readable line.
// When color is true, ANSI escape codes are added for visual distinction.
func formatStatusLineColor(st worktreeStatus, color bool) string {
	marker := " "
	if st.Current {
		marker = "*"
	}

	state := "clean"
	if st.Dirty {
		state = "dirty"
	}

	tracking := fmt.Sprintf("↑%d ↓%d", st.Ahead, st.Behind)
	if !st.HasUpstream {
		tracking = "no upstream"
	}

	if !color {
		return fmt.Sprintf("%s %-14s %-30s %-7s %s", marker, st.Branch, st.Path, state, tracking)
	}

	// Apply colors
	if st.Current {
		marker = colorize("*", ansiBold+";"+ansiCyanRaw)
	}

	branch := colorize(st.Branch, ansiBold)

	if st.Dirty {
		state = colorize("dirty", ansiRed)
	} else {
		state = colorize("clean", ansiGreen)
	}

	if !st.HasUpstream {
		tracking = colorize("no upstream", ansiDim)
	} else {
		aheadStr := fmt.Sprintf("↑%d", st.Ahead)
		behindStr := fmt.Sprintf("↓%d", st.Behind)
		if st.Ahead > 0 {
			aheadStr = colorize(aheadStr, ansiYellow)
		}
		if st.Behind > 0 {
			behindStr = colorize(behindStr, ansiYellow)
		}
		tracking = aheadStr + " " + behindStr
	}

	return fmt.Sprintf("%s %-14s %-30s %-7s %s", marker, branch, st.Path, state, tracking)
}

// gitStatusPorcelain runs git status --porcelain in the given directory.
func gitStatusPorcelain(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// gitRevListAheadBehind runs git rev-list --left-right --count HEAD...@{upstream} in the given directory.
func gitRevListAheadBehind(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

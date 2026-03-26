package main

import (
	"encoding/json"
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
	CI          string `json:"ci,omitempty"`
}

var statusCI bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status dashboard of all worktrees",
	Long: `Show a dashboard of all worktrees with their status.

For each worktree, shows: path, branch, dirty/clean state, and
ahead/behind counts relative to upstream.

Use --ci to include CI/CD pipeline status for each branch
(requires gh CLI for GitHub or glab CLI for GitLab).

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

		// Fetch CI status if requested
		if statusCI {
			ciStatuses := getCIStatuses(statuses)
			for i, ci := range ciStatuses {
				statuses[i].CI = ci
			}
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

	ci := ""
	if st.CI != "" {
		ci = " " + st.CI
	}

	if !color {
		return fmt.Sprintf("%s %-14s %-30s %-7s %s%s", marker, st.Branch, st.Path, state, tracking, ci)
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

	if st.CI != "" {
		ci = " " + formatCIColor(st.CI)
	}

	return fmt.Sprintf("%s %-14s %-30s %-7s %s%s", marker, branch, st.Path, state, tracking, ci)
}

// formatCIColor applies color to a CI status string.
func formatCIColor(ci string) string {
	switch ci {
	case "pass":
		return colorize("✓ CI", ansiGreen)
	case "fail":
		return colorize("✗ CI", ansiRed)
	case "pending":
		return colorize("● CI", ansiYellow)
	default:
		return colorize(ci, ansiDim)
	}
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

// getCIStatuses fetches CI check status for each worktree branch.
// It detects whether the repo uses GitHub or GitLab and calls the
// appropriate CLI tool. Returns a slice parallel to statuses.
func getCIStatuses(statuses []worktreeStatus) []string {
	results := make([]string, len(statuses))

	remoteType := detectCIRemoteType()
	if remoteType == RemoteUnknown {
		return results
	}

	for i, st := range statuses {
		if st.Branch == "" {
			continue
		}
		results[i] = fetchCIStatus(st.Branch, remoteType)
	}
	return results
}

// detectCIRemoteType checks if gh or glab CLI is available and
// whether the remote points to GitHub or GitLab.
func detectCIRemoteType() RemoteType {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return RemoteUnknown
	}
	url := strings.TrimSpace(string(output))

	if strings.Contains(url, "github.com") {
		if _, err := exec.LookPath("gh"); err == nil {
			return RemoteGitHub
		}
	}
	if strings.Contains(url, "gitlab") {
		if _, err := exec.LookPath("glab"); err == nil {
			return RemoteGitLab
		}
	}
	return RemoteUnknown
}

// fetchCIStatus returns the CI status for a single branch.
// Returns "pass", "fail", "pending", or "" if unavailable.
func fetchCIStatus(branch string, remoteType RemoteType) string {
	switch remoteType {
	case RemoteGitHub:
		return fetchGitHubCIStatus(branch)
	case RemoteGitLab:
		return fetchGitLabCIStatus(branch)
	default:
		return ""
	}
}

// fetchGitHubCIStatus uses gh to get the combined check status for a branch.
func fetchGitHubCIStatus(branch string) string {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/{owner}/{repo}/commits/%s/status", branch),
		"--jq", ".state")
	output, err := cmd.Output()
	if err != nil {
		// Fall back to check runs (GitHub Actions uses check runs, not commit statuses)
		return fetchGitHubCheckRuns(branch)
	}
	return normalizeGitHubState(strings.TrimSpace(string(output)))
}

// fetchGitHubCheckRuns uses gh to get check run conclusions for a branch.
func fetchGitHubCheckRuns(branch string) string {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/{owner}/{repo}/commits/%s/check-runs", branch),
		"--jq", ".check_runs | map(.conclusion) | unique | join(\",\")")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return normalizeGitHubCheckRuns(strings.TrimSpace(string(output)))
}

// normalizeGitHubState maps the GitHub combined status API state to our status.
func normalizeGitHubState(state string) string {
	switch state {
	case "success":
		return "pass"
	case "failure", "error":
		return "fail"
	case "pending":
		return "pending"
	default:
		return ""
	}
}

// normalizeGitHubCheckRuns maps GitHub check run conclusions to our status.
func normalizeGitHubCheckRuns(conclusions string) string {
	if conclusions == "" {
		return "pending"
	}
	for _, c := range strings.Split(conclusions, ",") {
		switch c {
		case "failure", "timed_out", "cancelled", "action_required":
			return "fail"
		case "null", "":
			return "pending"
		}
	}
	return "pass"
}

// fetchGitLabCIStatus uses glab to get the pipeline status for a branch.
func fetchGitLabCIStatus(branch string) string {
	cmd := exec.Command("glab", "ci", "status", "--branch", branch, "--output", "json")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return normalizeGitLabState(strings.TrimSpace(string(output)))
}

// normalizeGitLabState maps glab ci status JSON output to our status.
func normalizeGitLabState(jsonOutput string) string {
	// glab ci status --output json returns {"status":"success",...}
	var result struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &result); err != nil {
		return ""
	}
	switch result.Status {
	case "success":
		return "pass"
	case "failed":
		return "fail"
	case "running", "pending", "created", "waiting_for_resource", "preparing":
		return "pending"
	case "canceled", "skipped":
		return ""
	default:
		return ""
	}
}

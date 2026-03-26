package main

import (
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"
)

// parseCommitDate parses git log --format=%ci output into a time.Time.
func parseCommitDate(gitLogOutput string) (time.Time, error) {
	trimmed := strings.TrimSpace(gitLogOutput)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("empty commit date")
	}
	t, err := time.Parse("2006-01-02 15:04:05 -0700", trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse commit date %q: %w", trimmed, err)
	}
	return t, nil
}

// isRemoteBranchDeletedFromOutput checks if a branch is missing from git ls-remote output.
func isRemoteBranchDeletedFromOutput(branch string, lsRemoteOutput string) bool {
	if branch == "" {
		return true
	}
	target := "refs/heads/" + branch
	for _, line := range strings.Split(lsRemoteOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[1] == target {
			return false
		}
	}
	return true
}

// getLastCommitTime returns the time of the last commit in the given worktree path.
func getLastCommitTime(worktreePath string) (time.Time, error) {
	cmd := exec.Command("git", "-C", worktreePath, "log", "-1", "--format=%ci")
	output, err := cmd.Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get last commit time: %w", err)
	}
	return parseCommitDate(string(output))
}

// formatInactiveReason returns a human-readable reason string for inactive worktrees.
func formatInactiveReason(days int) string {
	return fmt.Sprintf("inactive (%d days)", days)
}

// classifyStaleWorktree determines if a worktree should be considered stale and returns the reason.
// Returns (isStale, reason). The main/master/default branch is never stale.
func classifyStaleWorktree(branch string, remoteDeleted bool, lastCommitTime time.Time, staleDays int, defaultBase string) (bool, string) {
	// Never flag main, master, or the configured default base branch
	if branch == "main" || branch == "master" || branch == defaultBase {
		return false, ""
	}

	// Remote deleted takes priority
	if remoteDeleted {
		return true, "remote deleted"
	}

	// Check commit age
	age := time.Since(lastCommitTime)
	ageDays := int(math.Floor(age.Hours() / 24))
	if ageDays > staleDays {
		return true, formatInactiveReason(ageDays)
	}

	return false, ""
}

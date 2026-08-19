package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var (
	cleanupDryRun    bool
	cleanupForce     bool
	cleanupStale     bool
	cleanupStaleDays int
)

// cleanupCandidate represents a worktree flagged for cleanup with a reason.
type cleanupCandidate struct {
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove worktrees for merged branches",
	Long: `Remove worktrees for branches that have been merged into the base branch.

This command finds all worktrees whose branches have been merged into main/master,
and removes them. Use --dry-run to preview what would be removed.

With --stale, also detect worktrees whose remote branch was deleted or whose
last commit is older than --stale-days (default 30).

Examples:
  wt cleanup              # Interactive confirmation for each worktree
  wt cleanup --dry-run    # Preview what would be removed
  wt cleanup --force      # Remove all without confirmation
  wt cleanup --stale      # Also detect stale worktrees
  wt cleanup --stale --stale-days 14  # Custom staleness threshold`,
	RunE: func(cmd *cobra.Command, args []string) error {
		base := getDefaultBase()
		jsonMode := isJSONOutput()

		// Get merged branches
		mergedBranches, err := getMergedBranches(base)
		if err != nil {
			return err
		}

		// Get existing worktree branches
		worktreeBranches, err := getExistingWorktreeBranches()
		if err != nil {
			return fmt.Errorf("failed to get worktrees: %w", err)
		}

		// Create a set of merged branches for quick lookup
		mergedSet := make(map[string]bool)
		for _, b := range mergedBranches {
			mergedSet[b] = true
		}

		// Build list of candidates with reasons
		var candidates []cleanupCandidate

		// Find worktrees for merged branches
		for _, branch := range worktreeBranches {
			if mergedSet[branch] {
				if path, exists := worktreeExists(branch); exists {
					candidates = append(candidates, cleanupCandidate{
						Branch: branch,
						Path:   path,
						Reason: "merged",
					})
				}
			}
		}

		// If --stale, also find stale worktrees
		if cleanupStale {
			// Build set of already-flagged branches to avoid duplicates
			flagged := make(map[string]bool)
			for _, c := range candidates {
				flagged[c.Branch] = true
			}

			// Get ls-remote output once for all branches
			lsRemoteCmd := exec.Command("git", "ls-remote", "--heads", "origin")
			lsRemoteOutput, lsRemoteErr := lsRemoteCmd.Output()
			lsRemoteStr := ""
			if lsRemoteErr == nil {
				lsRemoteStr = string(lsRemoteOutput)
			}

			for _, branch := range worktreeBranches {
				if flagged[branch] {
					continue
				}
				path, exists := worktreeExists(branch)
				if !exists {
					continue
				}

				// Check if remote branch is deleted
				remoteDeleted := false
				if lsRemoteErr == nil {
					remoteDeleted = isRemoteBranchDeletedFromOutput(branch, lsRemoteStr)
				}

				// Get last commit time
				lastCommitTime, commitErr := getLastCommitTime(path)
				if commitErr != nil {
					// Can't determine age, only flag if remote is deleted
					if remoteDeleted {
						stale, reason := classifyStaleWorktree(branch, remoteDeleted, lastCommitTime, cleanupStaleDays, base)
						if stale {
							candidates = append(candidates, cleanupCandidate{
								Branch: branch,
								Path:   path,
								Reason: reason,
							})
						}
					}
					continue
				}

				stale, reason := classifyStaleWorktree(branch, remoteDeleted, lastCommitTime, cleanupStaleDays, base)
				if stale {
					candidates = append(candidates, cleanupCandidate{
						Branch: branch,
						Path:   path,
						Reason: reason,
					})
				}
			}
		}

		if len(candidates) == 0 {
			if jsonMode {
				return emitJSONSuccess(cmd, map[string]any{"removed": 0, "skipped": 0, "base": base, "worktrees": []string{}})
			}
			if cleanupStale {
				fmt.Println("No worktrees found for merged or stale branches")
			} else {
				fmt.Println("No worktrees found for merged branches")
			}
			return nil
		}

		if jsonMode && !cleanupDryRun && !cleanupForce {
			return fmt.Errorf("wt cleanup with --format json requires --force or --dry-run")
		}

		// Dry run mode - just show what would be removed
		if cleanupDryRun {
			if jsonMode {
				planned := make([]map[string]string, 0, len(candidates))
				for _, c := range candidates {
					planned = append(planned, map[string]string{
						"branch": c.Branch,
						"path":   c.Path,
						"reason": c.Reason,
					})
				}
				return emitJSONSuccess(cmd, map[string]any{"dry_run": true, "base": base, "worktrees": planned})
			}
			fmt.Printf("Would remove %d worktree(s):\n", len(candidates))
			for _, c := range candidates {
				fmt.Printf("  - %s (%s) [%s]\n", c.Branch, c.Path, c.Reason)
			}
			return nil
		}

		// Track results
		removed := 0
		skipped := 0

		for _, c := range candidates {
			existingPath, exists := worktreeExists(c.Branch)
			if !exists {
				continue
			}

			// If not force mode, ask for confirmation
			if !cleanupForce {
				prompt := promptui.Prompt{
					Label:     fmt.Sprintf("Remove worktree for %s branch '%s'", c.Reason, c.Branch),
					IsConfirm: true,
					Stdout:    promptOutput(),
				}
				_, err := prompt.Run()
				if err != nil {
					fmt.Printf("  Skipped: %s\n", c.Branch)
					skipped++
					continue
				}
			}

			// Remove the worktree
			gitCmd := exec.Command("git", "worktree", "remove", existingPath)
			if !jsonMode {
				gitCmd.Stdout = os.Stdout
				gitCmd.Stderr = os.Stderr
			}
			if err := gitCmd.Run(); err != nil {
				if jsonMode {
					skipped++
					continue
				}
				fmt.Printf("  Failed to remove %s: %v\n", c.Branch, err)
				continue
			}

			if err := cleanupWorktreePath(existingPath); err != nil {
				if jsonMode {
					continue
				}
				fmt.Printf("  Warning: failed to cleanup path for %s: %v\n", c.Branch, err)
			}

			if !jsonMode {
				fmt.Printf("✓ Removed worktree: %s [%s]\n", c.Branch, c.Reason)
			}
			removed++
		}

		// Run prune at the end
		pruneGitCmd := exec.Command("git", "worktree", "prune")
		_ = pruneGitCmd.Run()

		if jsonMode {
			return emitJSONSuccess(cmd, map[string]any{"dry_run": false, "base": base, "removed": removed, "skipped": skipped})
		}

		fmt.Printf("\nCleanup complete: %d removed, %d skipped\n", removed, skipped)
		return nil
	},
}

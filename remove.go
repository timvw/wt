package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var removeForce bool

var removeCmd = &cobra.Command{
	Use:     "remove [branch]",
	Aliases: []string{"rm"},
	Short:   "Remove a worktree",
	Args:    cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var branch string
		jsonMode := isJSONOutput()

		// Interactive selection if no branch provided
		if len(args) == 0 {
			if jsonMode {
				return fmt.Errorf("wt remove with --format json requires an explicit branch argument")
			}
			branches, err := getExistingWorktreeBranches()
			if err != nil {
				return fmt.Errorf("failed to get worktrees: %w", err)
			}
			if len(branches) == 0 {
				return fmt.Errorf("no worktrees to remove")
			}

			prompt := promptui.Select{
				Label:             "Select worktree to remove",
				Items:             branches,
				Searcher:          fuzzySearcher(branches),
				StartInSearchMode: true,
			}
			_, result, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}
			branch = result
		} else {
			branch = args[0]
		}

		existingPath, exists := worktreeExists(branch)
		if !exists {
			return fmt.Errorf("no worktree found for branch: %s", branch)
		}

		// Build hook env for remove hooks
		info, _ := getRepoInfo()
		hookEnv := buildHookEnv(info, branch, existingPath)

		// Run pre-remove hooks
		if err := runHooks("pre_remove", getHooks("pre_remove"), hookEnv); err != nil {
			return fmt.Errorf("pre-remove hook failed: %w", err)
		}

		// Check if we're currently in the worktree being removed
		cwd, err := os.Getwd()
		inRemovedWorktree := err == nil && strings.HasPrefix(cwd, existingPath)

		// Find the main worktree path (for cd after removal)
		var mainWorktreePath string
		if inRemovedWorktree {
			listCmd := exec.Command("git", "worktree", "list")
			output, err := listCmd.Output()
			if err == nil {
				lines := strings.Split(string(output), "\n")
				if len(lines) > 0 {
					fields := strings.Fields(lines[0])
					if len(fields) > 0 {
						mainWorktreePath = fields[0]
					}
				}
			}
		}

		gitArgs := []string{"worktree", "remove"}
		if removeForce {
			gitArgs = append(gitArgs, "--force")
		}
		gitArgs = append(gitArgs, existingPath)

		gitCmd := exec.Command("git", gitArgs...)
		if !jsonMode {
			gitCmd.Stdout = os.Stdout
			gitCmd.Stderr = os.Stderr
		}
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to remove worktree: %w", err)
		}

		if err := cleanupWorktreePath(existingPath); err != nil {
			return err
		}

		// Run post-remove hooks (warn only)
		_ = runHooks("post_remove", getHooks("post_remove"), hookEnv)

		if jsonMode {
			return emitJSONSuccess(cmd, map[string]any{
				"status":      "removed",
				"branch":      branch,
				"path":        existingPath,
				"navigate_to": mainWorktreePath,
			})
		}

		fmt.Printf("✓ Removed worktree: %s\n", existingPath)

		// If we were in the removed worktree, navigate to main
		if inRemovedWorktree && mainWorktreePath != "" {
			printCDMarker(mainWorktreePath)
		}

		return nil
	},
}

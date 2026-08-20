package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"github.com/timvw/wt/internal/fuzzy"
)

var checkoutCmd = &cobra.Command{
	Use:     "checkout [branch]",
	Aliases: []string{"co"},
	Short:   "Checkout existing branch in new worktree",
	Args:    cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var branch string

		// Interactive selection if no branch provided
		if len(args) == 0 {
			if isJSONOutput() {
				return fmt.Errorf("wt checkout with --format json requires an explicit branch argument")
			}
			branches, err := getAvailableBranches()
			if err != nil {
				return fmt.Errorf("failed to get branches: %w", err)
			}
			if len(branches) == 0 {
				return fmt.Errorf("no available branches to checkout")
			}

			prompt := promptui.Select{
				Label:             "Select branch to checkout",
				Items:             branches,
				Searcher:          fuzzy.Searcher(branches),
				StartInSearchMode: true,
				Stdout:            promptOutput(),
			}
			_, result, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}
			branch = result
		} else {
			branch = args[0]
		}

		// Resolve case-insensitive match against remote branches
		resolved := resolveRemoteBranchCase(branch)
		if resolved != branch {
			fmt.Fprintf(os.Stderr, "Note: using remote branch name %q (you typed %q)\n", resolved, branch)
			branch = resolved
		}

		info, err := getRepoInfo()
		if err != nil {
			return err
		}

		// Check if worktree already exists
		if existingPath, exists := worktreeExists(branch); exists {
			hookEnv := buildHookEnv(info, branch, existingPath)

			// Run pre-checkout hooks
			if err := runHooks("pre_checkout", getHooks("pre_checkout"), hookEnv); err != nil {
				return fmt.Errorf("pre-checkout hook failed: %w", err)
			}

			// Run post-checkout hooks (warn only)
			_ = runHooks("post_checkout", getHooks("post_checkout"), hookEnv)

			if isJSONOutput() {
				return emitJSONSuccess(cmd, map[string]any{
					"status":      "exists",
					"branch":      branch,
					"path":        existingPath,
					"navigate_to": existingPath,
				})
			}
			fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
			printCDMarker(existingPath)
			return nil
		}

		// Check if branch exists
		if !branchExists(branch) {
			return fmt.Errorf("branch '%s' does not exist\nUse 'wt create %s' to create a new branch", branch, branch)
		}

		path, err := buildWorktreePath(info, branch)
		if err != nil {
			return err
		}
		warnIfCaseInsensitivePathCollision(path)

		hookEnv := buildHookEnv(info, branch, path)

		// Run pre-checkout hooks
		if err := runHooks("pre_checkout", getHooks("pre_checkout"), hookEnv); err != nil {
			return fmt.Errorf("pre-checkout hook failed: %w", err)
		}

		// Create worktree
		gitCmd := exec.Command("git", "worktree", "add", path, branch)
		if !isJSONOutput() {
			gitCmd.Stdout = os.Stdout
			gitCmd.Stderr = os.Stderr
		}
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		if !isJSONOutput() {
			fmt.Printf("✓ Worktree created at: %s\n", path)
		}

		// Materialise [files] before post_checkout hooks — see create.go.
		files := materialiseFiles(info, path, noCopyFiles)

		// Run post-checkout hooks (warn only)
		_ = runHooks("post_checkout", getHooks("post_checkout"), hookEnv)

		if isJSONOutput() {
			data := map[string]any{
				"status":      "created",
				"branch":      branch,
				"path":        path,
				"navigate_to": path,
			}
			if files != nil {
				data["files"] = files
			}
			return emitJSONSuccess(cmd, data)
		}

		printCDMarker(path)
		return nil
	},
}

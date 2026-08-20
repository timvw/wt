package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var createCmd = &cobra.Command{
	Use:   "create <branch> [base-branch]",
	Short: "Create new branch in worktree (default: main/master)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		branch := args[0]
		base := getDefaultBase()
		if len(args) > 1 {
			base = args[1]
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
			if isJSONOutput() {
				return emitJSONSuccess(cmd, map[string]any{
					"status":      "exists",
					"branch":      branch,
					"base":        base,
					"path":        existingPath,
					"navigate_to": existingPath,
				})
			}
			fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
			printCDMarker(existingPath)
			return nil
		}

		path, err := buildWorktreePath(info, branch)
		if err != nil {
			return err
		}
		warnIfCaseInsensitivePathCollision(path)

		hookEnv := buildHookEnv(info, branch, path)

		// Run pre-create hooks
		if err := runHooks("pre_create", getHooks("pre_create"), hookEnv); err != nil {
			return fmt.Errorf("pre-create hook failed: %w", err)
		}

		// Create new branch and worktree
		gitCmd := exec.Command("git", "worktree", "add", path, "-b", branch, base)
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

		// Materialise [files] before post_create hooks: a hook running
		// `npm install` or `direnv allow` has to see the .env that was just
		// copied. Failure here is non-fatal and never rolls back the worktree.
		files := materialiseFiles(info, path, noCopyFiles)

		// Run post-create hooks (warn only)
		_ = runHooks("post_create", getHooks("post_create"), hookEnv)

		if isJSONOutput() {
			data := map[string]any{
				"status":      "created",
				"branch":      branch,
				"base":        base,
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

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var prCmd = &cobra.Command{
	Use:   "pr [number|url]",
	Short: "Checkout GitHub PR in worktree (uses gh CLI)",
	Long: `Checkout a GitHub Pull Request in a worktree.

Looks up the PR's actual branch name using the 'gh' CLI, then creates
a worktree with that branch name — just like 'wt checkout <branch>'.
For GitLab Merge Requests, use 'wt mr' instead.

Examples:
  wt pr                                        # Interactive PR selection
  wt pr 123                                    # GitHub PR number
  wt pr https://github.com/org/repo/pull/123   # GitHub PR URL`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var input string

		// Interactive selection if no PR provided
		if len(args) == 0 {
			if isJSONOutput() {
				return fmt.Errorf("wt pr with --format json requires an explicit PR number or URL")
			}
			numbers, labels, err := getOpenPRs()
			if err != nil {
				return fmt.Errorf("failed to get PRs: %w (is 'gh' CLI installed?)", err)
			}
			if len(labels) == 0 {
				return fmt.Errorf("no open PRs found")
			}

			prompt := promptui.Select{
				Label:             "Select Pull Request",
				Items:             labels,
				Searcher:          fuzzySearcher(labels),
				StartInSearchMode: true,
			}
			idx, _, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}
			input = numbers[idx]
		} else {
			input = args[0]
		}

		return checkoutPROrMR(cmd, input, RemoteGitHub)
	},
}

var mrCmd = &cobra.Command{
	Use:   "mr [number|url]",
	Short: "Checkout GitLab MR in worktree (uses glab CLI)",
	Long: `Checkout a GitLab Merge Request in a worktree.

Looks up the MR's actual branch name using the 'glab' CLI, then creates
a worktree with that branch name — just like 'wt checkout <branch>'.
For GitHub Pull Requests, use 'wt pr' instead.

Examples:
  wt mr                                        # Interactive MR selection
  wt mr 123                                    # GitLab MR number
  wt mr https://gitlab.com/org/repo/-/merge_requests/123  # GitLab MR URL`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var input string

		// Interactive selection if no MR provided
		if len(args) == 0 {
			if isJSONOutput() {
				return fmt.Errorf("wt mr with --format json requires an explicit MR number or URL")
			}
			numbers, labels, err := getOpenMRs()
			if err != nil {
				return fmt.Errorf("failed to get MRs: %w (is 'glab' CLI installed?)", err)
			}
			if len(labels) == 0 {
				return fmt.Errorf("no open MRs found")
			}

			prompt := promptui.Select{
				Label:             "Select Merge Request",
				Items:             labels,
				Searcher:          fuzzySearcher(labels),
				StartInSearchMode: true,
			}
			idx, _, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}
			input = numbers[idx]
		} else {
			input = args[0]
		}

		return checkoutPROrMR(cmd, input, RemoteGitLab)
	},
}

func checkoutPROrMR(cmd *cobra.Command, input string, remoteType RemoteType) error {
	jsonMode := isJSONOutput()
	prNumber, err := getPRNumber(input)
	if err != nil {
		return err
	}

	var refSpec, prefix string

	switch remoteType {
	case RemoteGitHub:
		refSpec = fmt.Sprintf("pull/%s/head", prNumber)
		prefix = "pr"
		if _, err := exec.LookPath("gh"); err != nil {
			return fmt.Errorf("'gh' CLI not found. Install it from https://cli.github.com")
		}
	case RemoteGitLab:
		refSpec = fmt.Sprintf("merge-requests/%s/head", prNumber)
		prefix = "mr"
		if _, err := exec.LookPath("glab"); err != nil {
			return fmt.Errorf("'glab' CLI not found. Install it from https://gitlab.com/gitlab-org/cli")
		}
	default:
		return fmt.Errorf("invalid remote type")
	}

	// Look up the actual branch name from the PR/MR
	branch, err := getPRBranchName(prNumber, remoteType)
	if err != nil {
		return fmt.Errorf("failed to look up branch for %s #%s: %w", strings.ToUpper(prefix), prNumber, err)
	}

	info, err := getRepoInfo()
	if err != nil {
		return err
	}

	// Check if worktree already exists for this branch
	if existingPath, exists := worktreeExists(branch); exists {
		if jsonMode {
			return emitJSONSuccess(cmd, map[string]any{
				"status":      "exists",
				"id":          prNumber,
				"kind":        prefix,
				"branch":      branch,
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

	// Determine hook name based on remote type
	hookPrefix := "pr"
	if remoteType == RemoteGitLab {
		hookPrefix = "mr"
	}
	hookEnv := buildHookEnv(info, branch, path)

	// Run pre-pr/pre-mr hooks
	preHookName := "pre_" + hookPrefix
	if err := runHooks(preHookName, getHooks(preHookName), hookEnv); err != nil {
		return fmt.Errorf("%s hook failed: %w", preHookName, err)
	}

	// Try fetching the branch directly from origin
	fetchCmd := exec.Command("git", "fetch", "origin", branch)
	fetchCmd.Stderr = os.Stderr
	if err := fetchCmd.Run(); err != nil {
		// Fallback: fetch via PR/MR refspec (e.g. for fork PRs)
		fallbackCmd := exec.Command("git", "fetch", "origin", fmt.Sprintf("%s:%s", refSpec, branch))
		fallbackCmd.Stderr = os.Stderr
		_ = fallbackCmd.Run()
	}

	// Create worktree — prefer the remote-tracking branch, fall back to local
	var gitCmd *exec.Cmd
	if branchExists(branch) {
		gitCmd = exec.Command("git", "worktree", "add", path, branch)
	} else {
		gitCmd = exec.Command("git", "worktree", "add", path, "-b", branch, fmt.Sprintf("origin/%s", branch))
	}
	if !jsonMode {
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
	}
	if err := gitCmd.Run(); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	// Ensure upstream tracking is set (best-effort, may fail for fork PRs)
	upstreamCmd := exec.Command("git", "branch", "--set-upstream-to",
		fmt.Sprintf("origin/%s", branch), branch)
	_ = upstreamCmd.Run()

	// Run post-pr/post-mr hooks (warn only)
	postHookName := "post_" + hookPrefix
	_ = runHooks(postHookName, getHooks(postHookName), hookEnv)

	if jsonMode {
		return emitJSONSuccess(cmd, map[string]any{
			"status":      "created",
			"id":          prNumber,
			"kind":        prefix,
			"branch":      branch,
			"path":        path,
			"navigate_to": path,
		})
	}

	fmt.Printf("✓ %s #%s (%s) checked out at: %s\n", strings.ToUpper(prefix), prNumber, branch, path)

	printCDMarker(path)
	return nil
}

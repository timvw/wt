package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show worktree location configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonMode := isJSONOutput()
		pattern, err := resolveWorktreePattern()
		if err != nil {
			pattern = worktreePattern
			if pattern == "" {
				pattern = "unknown"
			}
		}

		configStatus := "not found, using defaults"
		if configFileFound {
			configStatus = "found"
		}

		repoConfigStatus := "not found"
		if configRepoFound {
			repoConfigStatus = "found"
		}

		hooks := map[string][]string{
			"pre_create":    worktreeHooks.PreCreate,
			"post_create":   worktreeHooks.PostCreate,
			"pre_checkout":  worktreeHooks.PreCheckout,
			"post_checkout": worktreeHooks.PostCheckout,
			"pre_remove":    worktreeHooks.PreRemove,
			"post_remove":   worktreeHooks.PostRemove,
			"pre_pr":        worktreeHooks.PrePR,
			"post_pr":       worktreeHooks.PostPR,
			"pre_mr":        worktreeHooks.PreMR,
			"post_mr":       worktreeHooks.PostMR,
		}

		if jsonMode {
			configData := map[string]string{
				"path":      configFilePath,
				"status":    configStatus,
				"strategy":  worktreeStrategy,
				"pattern":   pattern,
				"root":      worktreeRoot,
				"separator": worktreeSeparator,
			}
			if configRepoPath != "" {
				configData["repo_config_path"] = configRepoPath
				configData["repo_config_status"] = repoConfigStatus
			}
			return emitJSONSuccess(cmd, map[string]any{
				"config": configData,
				"strategies": []map[string]string{
					{"name": "global", "pattern": "{.worktreeRoot}/{.repo.Name}/{.branch}"},
					{"name": "sibling-repo", "pattern": "{.repo.Main}/../{.repo.Name}-{.branch}"},
					{"name": "parent-branches", "pattern": "{.repo.Main}/../{.branch}"},
					{"name": "parent-worktrees", "pattern": "{.repo.Main}/../{.repo.Name}.worktrees/{.branch}"},
					{"name": "parent-dotdir", "pattern": "{.repo.Main}/../.worktrees/{.branch}"},
					{"name": "inside-dotdir", "pattern": "{.repo.Main}/.worktrees/{.branch}"},
					{"name": "custom", "pattern": "requires pattern setting"},
				},
				"pattern_variables": []string{"{.repo.Name}", "{.repo.Main}", "{.repo.Owner}", "{.repo.Host}", "{.branch}", "{.worktreeRoot}", "{.env.VARNAME}"},
				"hooks":             hooks,
			})
		}

		repoLine := ""
		if configRepoPath != "" {
			repoLine = fmt.Sprintf("\nRepo cfg:  %s (%s)", configRepoPath, repoConfigStatus)
		}

		fmt.Printf(`Config:    %s (%s)%s

Strategy:  %s
Pattern:   %s
Root:      %s
Separator: %q

Strategies:
  global           -> {.worktreeRoot}/{.repo.Name}/{.branch}
  sibling-repo     -> {.repo.Main}/../{.repo.Name}-{.branch}
  parent-branches  -> {.repo.Main}/../{.branch}
  parent-worktrees -> {.repo.Main}/../{.repo.Name}.worktrees/{.branch}
  parent-dotdir    -> {.repo.Main}/../.worktrees/{.branch}
  inside-dotdir    -> {.repo.Main}/.worktrees/{.branch}
  custom           -> requires pattern setting

Pattern variables: {.repo.Name}, {.repo.Main}, {.repo.Owner}, {.repo.Host}, {.branch}, {.worktreeRoot}, {.env.VARNAME}
Note: The separator setting controls how "/" and "\" in value variables are replaced.
      Default "/" preserves slashes (nested dirs). Set to "-" or "_" for flat paths.
      Path variables ({.repo.Main}, {.worktreeRoot}) are never transformed.
Note: {.env.VARNAME} accesses the environment variable VARNAME (e.g. {.env.HOME}).
`, configFilePath, configStatus, repoLine, worktreeStrategy, pattern, worktreeRoot, worktreeSeparator)

		// Show configured hooks
		hookNames := []struct {
			name  string
			hooks []string
		}{
			{"pre_create", worktreeHooks.PreCreate},
			{"post_create", worktreeHooks.PostCreate},
			{"pre_checkout", worktreeHooks.PreCheckout},
			{"post_checkout", worktreeHooks.PostCheckout},
			{"pre_remove", worktreeHooks.PreRemove},
			{"post_remove", worktreeHooks.PostRemove},
			{"pre_pr", worktreeHooks.PrePR},
			{"post_pr", worktreeHooks.PostPR},
			{"pre_mr", worktreeHooks.PreMR},
			{"post_mr", worktreeHooks.PostMR},
		}
		hasHooks := false
		for _, h := range hookNames {
			if len(h.hooks) > 0 {
				hasHooks = true
				break
			}
		}
		if hasHooks {
			fmt.Println("Hooks:")
			for _, h := range hookNames {
				if len(h.hooks) > 0 {
					for _, cmd := range h.hooks {
						fmt.Printf("  %-15s %s\n", h.name+":", cmd)
					}
				}
			}
			fmt.Println()
		} else {
			fmt.Println("Hooks:    (none configured)")
			fmt.Println()
		}

		return nil
	},
}

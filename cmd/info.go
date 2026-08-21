package cmd

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
			"pre_clone":     worktreeHooks.PreClone,
			"post_clone":    worktreeHooks.PostClone,
		}

		filesInfo := describeFileConfig()

		if jsonMode {
			configData := map[string]string{
				"path":      configFilePath,
				"status":    configStatus,
				"strategy":  worktreeStrategy,
				"pattern":   pattern,
				"root":      worktreeRoot,
				"repo_root": reposRoot,
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
				"pattern_variables": []string{"{.repo.Name}", "{.repo.Main}", "{.repo.Owner}", "{.repo.Host}", "{.branch}", "{.worktreeRoot}", "{.env.VARNAME}", "{.env.VARNAME:-default}"},
				"hooks":             hooks,
				"files":             filesInfo,
				"repo_pattern": map[string]any{
					"value":     repoPattern,
					"variables": []string{"{.repoRoot}", "{.repo.Host}", "{.repo.Owner}", "{.repo.Name}", "{.branch}", "{.env.VARNAME}", "{.env.VARNAME:-default}"},
				},
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

Pattern variables: {.repo.Name}, {.repo.Main}, {.repo.Owner}, {.repo.Host}, {.branch}, {.worktreeRoot}, {.env.VARNAME}, {.env.VARNAME:-default}
Note: The separator setting controls how "/" and "\" in value variables are replaced.
      Default "/" preserves slashes (nested dirs). Set to "-" or "_" for flat paths.
      Path variables ({.repo.Main}, {.worktreeRoot}) are never transformed.
Note: {.env.VARNAME} accesses the environment variable VARNAME (e.g. {.env.HOME}).
      {.env.VARNAME:-fallback} uses "fallback" when VARNAME is unset.
`, configFilePath, configStatus, repoLine, worktreeStrategy, pattern, worktreeRoot, worktreeSeparator)

		// Show clone placement (used by wt clone)
		fmt.Printf("Repo root (wt clone):    %s\n", reposRoot)
		fmt.Printf("Repo pattern (wt clone): %s\n", repoPattern)
		fmt.Println("Repo pattern variables: {.repoRoot}, {.repo.Host}, {.repo.Owner}, {.repo.Name}, {.branch}, {.env.VARNAME}, {.env.VARNAME:-default}")
		fmt.Println()

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
			{"pre_clone", worktreeHooks.PreClone},
			{"post_clone", worktreeHooks.PostClone},
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

		printFilesSection(filesInfo)

		return nil
	},
}

// describeFileConfig resolves the effective [files] configuration for display.
//
// It reports rather than fails on a bad pattern: `wt info` is exactly where a
// user goes to find out why the copy refused to run, so it must still print.
func describeFileConfig() map[string]any {
	out := map[string]any{
		"copy_ignored": map[string]any{
			"value":  filesCopyIgnored,
			"source": configSources.CopyIgnored,
		},
		"copy":                  []layeredPattern{},
		"link":                  []layeredPattern{},
		"exclude":               []layeredPattern{},
		"worktreeinclude_found": false,
		"disabled":              filesDisabled(),
	}

	main := ""
	if info, err := getRepoInfo(); err == nil {
		main = info.Main
	}
	cfg, err := resolveFileConfig(main)
	if len(cfg.Copy) > 0 {
		out["copy"] = cfg.Copy
	}
	if len(cfg.Link) > 0 {
		out["link"] = cfg.Link
	}
	if len(cfg.Exclude) > 0 {
		out["exclude"] = cfg.Exclude
	}
	out["worktreeinclude_found"] = cfg.IncludeFileFound
	if cfg.IncludeFilePath != "" {
		out["worktreeinclude_path"] = cfg.IncludeFilePath
	}
	if err != nil {
		out["error"] = err.Error()
	}
	return out
}

// printFilesSection renders the Files block of `wt info`.
func printFilesSection(files map[string]any) {
	copyPatterns, _ := files["copy"].([]layeredPattern)
	linkPatterns, _ := files["link"].([]layeredPattern)
	excludePatterns, _ := files["exclude"].([]layeredPattern)
	copyIgnored, _ := files["copy_ignored"].(map[string]any)

	fmt.Println("Files:")
	fmt.Printf("  %-15s %v (%v)\n", "copy_ignored:", copyIgnored["value"], copyIgnored["source"])
	for _, group := range []struct {
		label    string
		patterns []layeredPattern
	}{
		{"copy", copyPatterns},
		{"link", linkPatterns},
		{"exclude", excludePatterns},
	} {
		for _, p := range group.patterns {
			fmt.Printf("  %-15s %-30s (%s)\n", group.label+":", p.Pattern, p.Source)
		}
	}

	if path, ok := files["worktreeinclude_path"].(string); ok {
		status := "not found"
		if found, _ := files["worktreeinclude_found"].(bool); found {
			status = "found"
		}
		fmt.Printf("  %-15s %s (%s)\n", worktreeIncludeFile+":", path, status)
	}
	if disabled, _ := files["disabled"].(bool); disabled {
		fmt.Printf("  %-15s copying is off (WT_FILES_DISABLED=1)\n", "status:")
	}
	if msg, ok := files["error"].(string); ok {
		fmt.Printf("  %-15s %s\n", "error:", msg)
	}
	if len(copyPatterns) == 0 && len(linkPatterns) == 0 && len(excludePatterns) == 0 {
		fmt.Println("  (nothing configured — see 'wt copy --help')")
	}
	fmt.Println()
}

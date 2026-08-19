package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var configInitForce bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage wt configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printCommandHelp(cmd)
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a default configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := resolveConfigPath(configFlag)
		if err := writeDefaultConfig(path, configInitForce); err != nil {
			return err
		}
		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]string{"path": path, "status": "created"})
		}
		fmt.Printf("Created config file: %s\n", path)
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show effective configuration with sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := configShowPatternValue()

		configStatus := "not found"
		if configFileFound {
			configStatus = "found"
		}

		if isJSONOutput() {
			data := map[string]any{
				"config_file": map[string]string{
					"path":   configFilePath,
					"status": configStatus,
				},
				"effective": map[string]any{
					"root":         map[string]string{"value": worktreeRoot, "source": configSources.Root},
					"repo_root":    map[string]string{"value": reposRoot, "source": configSources.RepoRoot},
					"strategy":     map[string]string{"value": worktreeStrategy, "source": configSources.Strategy},
					"pattern":      map[string]string{"value": pattern, "source": configSources.Pattern},
					"separator":    map[string]string{"value": worktreeSeparator, "source": configSources.Separator},
					"repo_pattern": map[string]string{"value": repoPattern, "source": configSources.RepoPattern},
				},
			}
			if configRepoFound {
				data["repo_config"] = map[string]string{
					"path":   configRepoPath,
					"status": "found",
				}
			}
			return emitJSONSuccess(cmd, data)
		}

		fmt.Printf("Config file: %s (%s)\n", configFilePath, configStatus)
		if configRepoFound {
			fmt.Printf("Repo config: %s (found)\n", configRepoPath)
		}
		fmt.Println()
		rows := []struct{ key, value, source string }{
			{"root", worktreeRoot, configSources.Root},
			{"repo_root", reposRoot, configSources.RepoRoot},
			{"strategy", worktreeStrategy, configSources.Strategy},
			{"pattern", pattern, configSources.Pattern},
			{"separator", fmt.Sprintf("%q", worktreeSeparator), configSources.Separator},
			{"repo_pattern", repoPattern, configSources.RepoPattern},
		}
		// Size the value column to the widest value (min 40) so long patterns
		// don't push their source marker out of the column.
		width := 40
		for _, r := range rows {
			if len(r.value) > width {
				width = len(r.value)
			}
		}
		fmt.Printf("Effective configuration:\n")
		for _, r := range rows {
			fmt.Printf("  %-16s = %-*s (%s)\n", r.key, width, r.value, r.source)
		}
		return nil
	},
}

func configShowPatternValue() string {
	pattern, err := resolveWorktreePattern()
	if err == nil {
		return pattern
	}

	return "(none)"
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	RunE: func(cmd *cobra.Command, args []string) error {
		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]string{"path": resolveConfigPath(configFlag)})
		}
		fmt.Println(resolveConfigPath(configFlag))
		return nil
	},
}

package main

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

		repoConfigStatus := "not found"
		if configRepoFound {
			repoConfigStatus = "found"
		}

		if isJSONOutput() {
			data := map[string]any{
				"config_file": map[string]string{
					"path":   configFilePath,
					"status": configStatus,
				},
				"effective": map[string]any{
					"root":      map[string]string{"value": worktreeRoot, "source": configSources.Root},
					"strategy":  map[string]string{"value": worktreeStrategy, "source": configSources.Strategy},
					"pattern":   map[string]string{"value": pattern, "source": configSources.Pattern},
					"separator": map[string]string{"value": worktreeSeparator, "source": configSources.Separator},
				},
			}
			if configRepoPath != "" {
				data["repo_config"] = map[string]string{
					"path":   configRepoPath,
					"status": repoConfigStatus,
				}
			}
			return emitJSONSuccess(cmd, data)
		}

		fmt.Printf("Config file: %s (%s)\n", configFilePath, configStatus)
		if configRepoPath != "" {
			fmt.Printf("Repo config: %s (%s)\n", configRepoPath, repoConfigStatus)
		}
		fmt.Println()
		fmt.Printf("Effective configuration:\n")
		fmt.Printf("  %-10s = %-40s (%s)\n", "root", worktreeRoot, configSources.Root)
		fmt.Printf("  %-10s = %-40s (%s)\n", "strategy", worktreeStrategy, configSources.Strategy)
		fmt.Printf("  %-10s = %-40s (%s)\n", "pattern", pattern, configSources.Pattern)
		fmt.Printf("  %-10s = %-40s (%s)\n", "separator", fmt.Sprintf("%q", worktreeSeparator), configSources.Separator)
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

package cmd

import (
	"fmt"
	"os"
	"strings"

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
		hooksPolicyValue, hooksPolicySource := resolveHooksPolicy()
		copyList, linkList, excludeList, filesErr := resolvedFileLists()

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
					"hooks_policy": map[string]string{"value": hooksPolicyValue, "source": hooksPolicySource},
					"copy_ignored": map[string]string{"value": fmt.Sprintf("%t", filesCopyIgnored), "source": configSources.CopyIgnored},
					// The lists are summarised, not enumerated: `wt info` prints
					// them pattern by pattern. This answers "is my git config
					// being read at all", which is what sends people here.
					"copy":    listSummary(copyList),
					"link":    listSummary(linkList),
					"exclude": listSummary(excludeList),
				},
			}
			if configRepoFound {
				data["repo_config"] = map[string]string{
					"path":   configRepoPath,
					"status": "found",
				}
			}
			if filesErr != nil {
				data["files_error"] = filesErr.Error()
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
			{"hooks_policy", hooksPolicyValue, hooksPolicySource},
			{"copy_ignored", fmt.Sprintf("%t", filesCopyIgnored), configSources.CopyIgnored},
		}
		for _, list := range []struct {
			key      string
			patterns []layeredPattern
		}{
			{"copy", copyList}, {"link", linkList}, {"exclude", excludeList},
		} {
			s := listSummary(list.patterns)
			rows = append(rows, struct{ key, value, source string }{list.key, s["value"], s["source"]})
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
		if filesErr != nil {
			// stderr, so the table stays clean when it is piped. Printed after
			// the rows because it qualifies the three [files] counts above it,
			// which may be short by a whole layer.
			fmt.Fprintf(os.Stderr, "⚠ [files]: %v\n", filesErr)
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

// resolvedFileLists returns the three [files] lists with every layer folded in,
// which is one more than the loader globals hold: .worktreeinclude lives at the
// main worktree root and is read later, by resolveFileConfig.
//
// `wt info` resolves the same way, and the two commands disagreeing about how
// many patterns are in effect would send the reader looking for a bug that is
// not there.
//
// The error is returned rather than swallowed because it decides whether the
// counts mean anything. A validation error leaves them intact — the lists are
// populated before validateFilePatterns runs. An unreadable, malformed or
// symlinked .worktreeinclude does not: resolveFileConfig gives up before
// folding that layer in, so the counts are short by exactly the layer this
// helper exists to include.
func resolvedFileLists() (copyList, linkList, excludeList []layeredPattern, err error) {
	main := ""
	if info, repoErr := getRepoInfo(); repoErr == nil {
		main = info.Main
	}
	cfg, err := resolveFileConfig(main)
	return cfg.Copy, cfg.Link, cfg.Exclude, err
}

// listSummary renders one [files] list as a count and the layers that fed it,
// in the shape the scalar rows use.
//
// A count rather than the patterns themselves: the lists accumulate, so there
// can be many, and no single one of them is "the effective value" the way a
// scalar has one. The layers are what the reader is usually after — "is my
// .git/config being read at all" is the question that sends people to
// `wt config show`. `wt info` prints the patterns individually.
//
// The sources are those of the *effective* patterns, in the order the layers
// apply, with a repeated layer name listed once rather than padding the column.
// A layer whose every pattern was already contributed by a lower one therefore
// does not appear: the patterns are deduplicated and each is credited to the
// first layer that supplied it, the same rule `wt info` displays. Such a layer
// has not changed the effective list, so there is nothing for it to be the
// source of.
func listSummary(patterns []layeredPattern) map[string]string {
	if len(patterns) == 0 {
		return map[string]string{"value": "(none)", "source": "default"}
	}

	var sources []string
	seen := map[string]bool{}
	for _, p := range patterns {
		if seen[p.Source] {
			continue
		}
		seen[p.Source] = true
		sources = append(sources, p.Source)
	}

	unit := "patterns"
	if len(patterns) == 1 {
		unit = "pattern"
	}
	return map[string]string{
		"value":  fmt.Sprintf("%d %s", len(patterns), unit),
		"source": strings.Join(sources, ", "),
	}
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

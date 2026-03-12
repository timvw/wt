package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type usageExample struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

type exampleTopic struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Examples    []usageExample `json:"examples"`
}

var exampleCatalog = map[string]exampleTopic{
	"checkout": {
		Name:        "checkout",
		Description: "Checkout an existing branch in a worktree",
		Examples: []usageExample{
			{Command: "wt checkout feature-branch", Description: "Checkout an existing branch in a new worktree"},
			{Command: "wt co", Description: "Interactively select a branch to checkout"},
			{Command: "wt --format json checkout feature-branch", Description: "Machine-readable output; does not auto-navigate shell"},
		},
	},
	"create": {
		Name:        "create",
		Description: "Create a new branch in a worktree",
		Examples: []usageExample{
			{Command: "wt create my-feature", Description: "Create branch from default base (main/master)"},
			{Command: "wt create my-feature develop", Description: "Create branch from explicit base branch"},
			{Command: "wt --format json create my-feature", Description: "Machine-readable output; does not auto-navigate shell"},
		},
	},
	"pr": {
		Name:        "pr",
		Description: "Checkout GitHub PR branch in a worktree",
		Examples: []usageExample{
			{Command: "wt pr 123", Description: "Checkout GitHub PR #123"},
			{Command: "wt pr https://github.com/org/repo/pull/123", Description: "Checkout PR by URL"},
			{Command: "wt --format json pr 123", Description: "Machine-readable output; does not auto-navigate shell"},
		},
	},
	"mr": {
		Name:        "mr",
		Description: "Checkout GitLab MR branch in a worktree",
		Examples: []usageExample{
			{Command: "wt mr 123", Description: "Checkout GitLab MR !123"},
			{Command: "wt mr https://gitlab.com/org/repo/-/merge_requests/123", Description: "Checkout MR by URL"},
			{Command: "wt --format json mr 123", Description: "Machine-readable output; does not auto-navigate shell"},
		},
	},
	"list": {
		Name:        "list",
		Description: "List all worktrees",
		Examples: []usageExample{
			{Command: "wt list", Description: "List all worktrees"},
			{Command: "wt ls", Description: "Short alias for list"},
			{Command: "wt --format json list", Description: "Machine-readable worktree listing"},
		},
	},
	"remove": {
		Name:        "remove",
		Description: "Remove a worktree",
		Examples: []usageExample{
			{Command: "wt remove old-branch", Description: "Remove worktree for old-branch"},
			{Command: "wt rm", Description: "Interactively select a worktree to remove"},
			{Command: "wt --format json remove old-branch", Description: "Machine-readable output; does not auto-navigate shell"},
		},
	},
	"cleanup": {
		Name:        "cleanup",
		Description: "Remove worktrees for merged branches",
		Examples: []usageExample{
			{Command: "wt cleanup", Description: "Remove merged branch worktrees with confirmation"},
			{Command: "wt cleanup --dry-run", Description: "Preview what would be removed"},
			{Command: "wt --format json cleanup --force", Description: "Machine-readable cleanup summary"},
		},
	},
	"migrate": {
		Name:        "migrate",
		Description: "Migrate existing worktrees to configured paths",
		Examples: []usageExample{
			{Command: "wt migrate", Description: "Move worktrees to paths derived from current config"},
			{Command: "wt migrate --force", Description: "Replace non-empty targets during migration"},
		},
	},
	"info": {
		Name:        "info",
		Description: "Show active worktree placement configuration",
		Examples: []usageExample{
			{Command: "wt info", Description: "Show strategy, pattern, and hooks"},
			{Command: "wt --format json info", Description: "Machine-readable configuration details"},
		},
	},
	"config": {
		Name:        "config",
		Description: "Manage configuration file",
		Examples: []usageExample{
			{Command: "wt config show", Description: "Show effective configuration and value sources"},
			{Command: "wt config path", Description: "Print config file path"},
			{Command: "wt --format json config show", Description: "Machine-readable config details"},
		},
	},
	"version": {
		Name:        "version",
		Description: "Show wt version",
		Examples: []usageExample{
			{Command: "wt version", Description: "Print current wt version"},
			{Command: "wt --format json version", Description: "Machine-readable version output"},
		},
	},
}

var topicAliases = map[string]string{
	"co":            "checkout",
	"ls":            "list",
	"rm":            "remove",
	"configuration": "config",
}

func canonicalTopic(input string) string {
	t := strings.ToLower(strings.TrimSpace(input))
	if alias, ok := topicAliases[t]; ok {
		return alias
	}
	return t
}

func sortedTopics() []string {
	names := make([]string, 0, len(exampleCatalog))
	for k := range exampleCatalog {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func renderExamplesText(topic *exampleTopic) {
	fmt.Println("wt examples")
	fmt.Println()
	fmt.Println("Use examples to discover common workflows and assistant-friendly invocations.")
	fmt.Println("Note: commands run with --format json are machine-readable and do not auto-navigate your shell.")
	fmt.Println()

	if topic == nil {
		fmt.Println("Topics:")
		for _, name := range sortedTopics() {
			t := exampleCatalog[name]
			fmt.Printf("  %-10s %s\n", t.Name, t.Description)
		}
		fmt.Println()
		fmt.Println("Show one topic:")
		fmt.Println("  wt examples create")
		return
	}

	fmt.Printf("Topic: %s\n", topic.Name)
	fmt.Printf("Description: %s\n\n", topic.Description)
	fmt.Println("Examples:")
	for _, ex := range topic.Examples {
		fmt.Printf("  %s\n", ex.Command)
		fmt.Printf("    %s\n", ex.Description)
	}
}

var examplesCmd = &cobra.Command{
	Use:   "examples [topic]",
	Short: "Show practical command examples",
	Long: `Show practical usage examples for wt commands.

Run without arguments to list topics, or pass a command/topic name
to show focused examples.

Examples:
  wt examples
  wt examples create
  wt examples rm
  wt --format json examples create`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if isJSONOutput() {
				topics := make([]exampleTopic, 0, len(exampleCatalog))
				for _, name := range sortedTopics() {
					t := exampleCatalog[name]
					t.Examples = nil
					topics = append(topics, t)
				}
				return emitJSONSuccess(cmd, map[string]any{"topics": topics})
			}
			renderExamplesText(nil)
			return nil
		}

		name := canonicalTopic(args[0])
		topic, ok := exampleCatalog[name]
		if !ok {
			return fmt.Errorf("unknown examples topic %q", args[0])
		}

		if isJSONOutput() {
			return emitJSONSuccess(cmd, topic)
		}
		renderExamplesText(&topic)
		return nil
	},
}

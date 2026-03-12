package main

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

type usageExample struct {
	Command       string   `json:"command"`
	Purpose       string   `json:"purpose"`
	Outcome       string   `json:"outcome"`
	ExitCode      string   `json:"exit_code"`
	PathExample   string   `json:"path_example,omitempty"`
	PathBasis     string   `json:"path_basis,omitempty"`
	Preconditions []string `json:"preconditions,omitempty"`
	SideEffects   []string `json:"side_effects,omitempty"`
	FailureModes  []string `json:"failure_modes,omitempty"`
	FollowUp      []string `json:"follow_up,omitempty"`
	Notes         []string `json:"notes,omitempty"`
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
			{
				Command:       "wt checkout feature-branch",
				Purpose:       "Create or reuse a worktree for an existing local branch.",
				Outcome:       "Worktree for feature-branch exists and branch is checked out there.",
				ExitCode:      "0 on success; non-zero if branch does not exist or git worktree creation fails.",
				PathExample:   "$WORKTREE_ROOT/<repo>/feature-branch (existing or created)",
				PathBasis:     "Derived from active pattern in wt info; this example assumes default global strategy.",
				SideEffects:   []string{"In text mode with shellenv, wrapper may auto-navigate to target path.", "In --format json mode, wrapper does not auto-navigate."},
				FailureModes:  []string{"Branch does not exist: create it first or use wt create.", "Worktree add failure: inspect git worktree list and path conflicts."},
				FollowUp:      []string{"wt list", "wt remove feature-branch"},
				Preconditions: []string{"Run inside a git repository."},
			},
			{
				Command:     "wt --format json checkout feature-branch",
				Purpose:     "Machine-readable checkout flow for automation.",
				Outcome:     "JSON envelope describing whether worktree was created or already existed, including navigate_to path.",
				ExitCode:    "0 on success; non-zero on failure.",
				SideEffects: []string{"No auto-navigation marker is printed.", "stdout stays JSON-only."},
				FollowUp:    []string{"Parse data.navigate_to if your tool wants to cd explicitly."},
			},
		},
	},
	"create": {
		Name:        "create",
		Description: "Create a new branch in a worktree",
		Examples: []usageExample{
			{
				Command:       "wt create my-feature",
				Purpose:       "Create a new branch from default base (main/master) and create worktree.",
				Outcome:       "New branch exists, worktree directory is created, branch checked out there.",
				ExitCode:      "0 on success; non-zero if base is missing or branch/path conflicts.",
				PathExample:   "$WORKTREE_ROOT/<repo>/my-feature (created)",
				PathBasis:     "Derived from active pattern in wt info; this example assumes default global strategy.",
				Preconditions: []string{"Repository has main or master (or use explicit base argument)."},
				SideEffects:   []string{"Runs configured post_create/post_checkout hooks.", "Text mode + shellenv may auto-navigate."},
				FailureModes:  []string{"Base branch missing: use wt create my-feature <base>.", "Worktree path conflict: inspect existing worktrees with wt list."},
				FollowUp:      []string{"wt list", "wt remove my-feature"},
			},
			{
				Command:      "wt --format json create my-feature",
				Purpose:      "Automation-friendly branch/worktree creation.",
				Outcome:      "JSON envelope with status, branch, base, path, and navigate_to.",
				ExitCode:     "0 on success; non-zero on failure.",
				SideEffects:  []string{"No auto-navigation marker in output.", "stdout remains machine-readable JSON."},
				FailureModes: []string{"Same branch already has worktree: status may be exists; parse returned path."},
			},
		},
	},
	"pr": {
		Name:        "pr",
		Description: "Checkout GitHub PR branch in a worktree",
		Examples: []usageExample{
			{
				Command:       "wt pr 123",
				Purpose:       "Fetch PR branch from GitHub and create a worktree from it.",
				Outcome:       "Local branch pr-123 exists and worktree is checked out at that branch.",
				ExitCode:      "0 on success; non-zero if gh/git operations fail.",
				Preconditions: []string{"gh CLI installed and authenticated for repo access."},
				FailureModes:  []string{"PR not found or inaccessible.", "Network/auth issues with GitHub."},
				FollowUp:      []string{"wt list", "wt remove pr-123"},
			},
			{
				Command:      "wt --format json pr 123",
				Purpose:      "Machine-readable PR checkout for tooling.",
				Outcome:      "JSON envelope with status, id, branch, path, navigate_to.",
				ExitCode:     "0 on success; non-zero on failure.",
				SideEffects:  []string{"No auto-navigation in wrapper when --format json is present."},
				FailureModes: []string{"Interactive PR selection is not supported in JSON mode; pass number or URL."},
			},
		},
	},
	"mr": {
		Name:        "mr",
		Description: "Checkout GitLab MR branch in a worktree",
		Examples: []usageExample{
			{
				Command:       "wt mr 123",
				Purpose:       "Fetch GitLab MR branch and create a worktree.",
				Outcome:       "Local branch mr-123 exists and worktree is checked out at that branch.",
				ExitCode:      "0 on success; non-zero if glab/git operations fail.",
				Preconditions: []string{"glab CLI installed and authenticated."},
				FailureModes:  []string{"MR not found or inaccessible.", "Network/auth issues with GitLab."},
				FollowUp:      []string{"wt list", "wt remove mr-123"},
			},
			{
				Command:      "wt --format json mr 123",
				Purpose:      "Machine-readable MR checkout.",
				Outcome:      "JSON envelope with status, id, branch, path, and navigate_to.",
				ExitCode:     "0 on success; non-zero on failure.",
				SideEffects:  []string{"No wrapper auto-navigation in JSON mode."},
				FailureModes: []string{"Interactive MR selection is not supported in JSON mode; pass number or URL."},
			},
		},
	},
	"list": {
		Name:        "list",
		Description: "List all worktrees",
		Examples: []usageExample{
			{
				Command:      "wt list",
				Purpose:      "Inspect currently registered git worktrees.",
				Outcome:      "Text table from git worktree list.",
				ExitCode:     "0 on success.",
				FollowUp:     []string{"wt remove <branch>", "wt cleanup"},
				FailureModes: []string{"Non-git directory: command fails."},
			},
			{
				Command:     "wt --format json list",
				Purpose:     "Structured worktree inventory for scripts and assistants.",
				Outcome:     "JSON envelope containing data.worktrees parsed from git porcelain output.",
				ExitCode:    "0 on success.",
				SideEffects: []string{"No side effects; read-only command."},
			},
		},
	},
	"remove": {
		Name:        "remove",
		Description: "Remove a worktree",
		Examples: []usageExample{
			{
				Command:       "wt remove old-branch",
				Purpose:       "Delete worktree for branch and clean up directory bookkeeping.",
				Outcome:       "Branch worktree path is removed; shell wrapper may navigate back to main worktree in text mode.",
				ExitCode:      "0 on success; non-zero if branch has no worktree or removal fails.",
				PathExample:   "$WORKTREE_ROOT/<repo>/old-branch -> (removed)",
				PathBasis:     "Derived from active pattern in wt info; this example assumes default global strategy.",
				Preconditions: []string{"Target branch currently has a worktree."},
				FailureModes:  []string{"Dirty worktree requires --force.", "No matching worktree for branch."},
				FollowUp:      []string{"wt list"},
			},
			{
				Command:      "wt --format json remove old-branch",
				Purpose:      "Machine-readable removal flow.",
				Outcome:      "JSON envelope with removed path and navigate_to target.",
				ExitCode:     "0 on success; non-zero on failure.",
				FailureModes: []string{"JSON mode requires explicit branch argument; no interactive selector."},
				SideEffects:  []string{"No auto-navigation marker in stdout."},
			},
		},
	},
	"cleanup": {
		Name:        "cleanup",
		Description: "Remove worktrees for merged branches",
		Examples: []usageExample{
			{
				Command:      "wt cleanup --dry-run",
				Purpose:      "Preview merged-branch worktrees that would be removed.",
				Outcome:      "Lists candidate worktrees without deleting them.",
				ExitCode:     "0 on success.",
				PathExample:  "$WORKTREE_ROOT/<repo>/<merged-branch> -> (candidate for removal)",
				PathBasis:    "Candidates are discovered from merged branches and mapped through active pattern.",
				SideEffects:  []string{"No deletions in dry-run mode."},
				FollowUp:     []string{"wt cleanup --force"},
				FailureModes: []string{"Merge-base detection issues if repository state is unusual."},
			},
			{
				Command:      "wt --format json cleanup --force",
				Purpose:      "Batch cleanup with machine-readable summary.",
				Outcome:      "JSON envelope with removed and skipped counters.",
				ExitCode:     "0 on success; non-zero on errors.",
				FailureModes: []string{"In JSON mode, cleanup requires --force or --dry-run."},
			},
		},
	},
	"migrate": {
		Name:        "migrate",
		Description: "Migrate existing worktrees to configured paths",
		Examples: []usageExample{
			{
				Command:       "wt migrate",
				Purpose:       "Move managed worktrees to paths derived from current configuration.",
				Outcome:       "Worktrees are moved where possible; non-empty target paths are skipped.",
				ExitCode:      "0 when migration operation completes; non-zero on fatal errors.",
				PathExample:   "$WORKTREE_ROOT_OLD/<repo>/<branch> -> $WORKTREE_ROOT_NEW/<repo>/<branch>",
				PathBasis:     "Source and destination paths are computed from old/new active pattern and variables.",
				Preconditions: []string{"Set desired strategy/pattern first (wt config show / env vars)."},
				FailureModes:  []string{"Target path already exists and is non-empty.", "Filesystem move/rename failures."},
				FollowUp:      []string{"wt list", "wt info"},
			},
			{
				Command:     "wt migrate --force",
				Purpose:     "Allow replacement of non-empty targets during migration.",
				Outcome:     "Worktrees are migrated even when targets already contain files.",
				ExitCode:    "0 on success; non-zero on unrecoverable failures.",
				SideEffects: []string{"May overwrite data at target worktree locations."},
				Notes:       []string{"Use with caution; verify destination layout before running."},
			},
		},
	},
	"info": {
		Name:        "info",
		Description: "Show active worktree placement configuration",
		Examples: []usageExample{
			{
				Command:     "wt info",
				Purpose:     "Inspect current strategy, pattern variables, and configured hooks.",
				Outcome:     "Human-readable report of active placement configuration.",
				ExitCode:    "0 on success.",
				SideEffects: []string{"Read-only command."},
			},
			{
				Command:     "wt --format json info",
				Purpose:     "Get structured config metadata for automation.",
				Outcome:     "JSON envelope with config, strategies, pattern variables, and hooks.",
				ExitCode:    "0 on success.",
				SideEffects: []string{"Read-only command."},
			},
		},
	},
	"config": {
		Name:        "config",
		Description: "Manage configuration file",
		Examples: []usageExample{
			{
				Command:      "wt config show",
				Purpose:      "Inspect effective config values and their sources.",
				Outcome:      "Shows config file path/status and resolved settings.",
				ExitCode:     "0 on success.",
				SideEffects:  []string{"Read-only command."},
				FailureModes: []string{"Malformed config file may produce parse errors."},
			},
			{
				Command:      "wt config init",
				Purpose:      "Create default config file.",
				Outcome:      "Config file is created unless it already exists.",
				ExitCode:     "0 on success; non-zero if config exists and --force was not provided.",
				FailureModes: []string{"Permission issues when writing config path."},
				FollowUp:     []string{"wt config show", "wt info"},
			},
			{
				Command:     "wt --format json config show",
				Purpose:     "Structured config introspection for tools.",
				Outcome:     "JSON envelope with effective values and source information.",
				ExitCode:    "0 on success.",
				SideEffects: []string{"Read-only command."},
			},
		},
	},
	"version": {
		Name:        "version",
		Description: "Show wt version",
		Examples: []usageExample{
			{
				Command:     "wt version",
				Purpose:     "Print current wt version for troubleshooting and automation checks.",
				Outcome:     "Outputs wt version string.",
				ExitCode:    "0 on success.",
				SideEffects: []string{"Read-only command."},
			},
			{
				Command:     "wt --format json version",
				Purpose:     "Expose version in machine-readable envelope.",
				Outcome:     "JSON with data.version.",
				ExitCode:    "0 on success.",
				SideEffects: []string{"Read-only command."},
			},
		},
	},
}

func sortedTopics() []string {
	names := make([]string, 0, len(exampleCatalog))
	for k := range exampleCatalog {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func orderedTopics() []exampleTopic {
	result := make([]exampleTopic, 0, len(exampleCatalog))
	for _, name := range sortedTopics() {
		result = append(result, exampleCatalog[name])
	}
	return result
}

func printListSection(title string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Printf("      %s:\n", title)
	for _, v := range values {
		fmt.Printf("        - %s\n", v)
	}
}

func renderExamplesText(topics []exampleTopic) {
	fmt.Println("wt examples")
	fmt.Println()
	fmt.Println("Runnable usage examples with expected outcomes.")
	fmt.Println("This command intentionally prints the full catalog; filter with rg/grep if desired.")
	fmt.Println("Note: --format json output is machine-readable and does not auto-navigate your shell.")
	fmt.Println()

	for _, topic := range topics {
		fmt.Printf("%s: %s\n", topic.Name, topic.Description)
		for _, ex := range topic.Examples {
			fmt.Printf("  %s\n", ex.Command)
			fmt.Printf("    => %s\n", ex.Outcome)
			fmt.Printf("    exit: %s\n", ex.ExitCode)
			if len(ex.Preconditions) > 0 {
				fmt.Printf("    preconditions: %s\n", ex.Preconditions[0])
			}
			if ex.PathExample != "" {
				fmt.Printf("    path example: %s\n", ex.PathExample)
			}
			if ex.PathBasis != "" {
				fmt.Printf("    path basis: %s\n", ex.PathBasis)
			}
			if len(ex.FailureModes) > 0 {
				fmt.Printf("    common failure: %s\n", ex.FailureModes[0])
			}
			if len(ex.FollowUp) > 0 {
				fmt.Printf("    follow-up: %s\n", ex.FollowUp[0])
			}
			if len(ex.Notes) > 0 {
				fmt.Printf("    note: %s\n", ex.Notes[0])
			}
			fmt.Println()
		}
	}
}

var examplesCmd = &cobra.Command{
	Use:   "examples",
	Short: "Show detailed command examples and outcomes",
	Long: `Show a full catalog of wt command examples, including expected outcomes,
side effects, failure modes, and follow-up actions.

This command intentionally prints all topics by default. Use grep/rg if you
want to filter specific commands.

Examples:
  wt examples
  wt --format json examples`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		topics := orderedTopics()
		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{
				"catalog_scope": "full",
				"notes": []string{
					"The examples catalog is intentionally full and unfiltered.",
					"In --format json mode, shell wrappers must not auto-navigate.",
				},
				"topics": topics,
			})
		}
		renderExamplesText(topics)
		return nil
	},
}

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type usageExample struct {
	Command       string   `json:"command"`
	Purpose       string   `json:"purpose"`
	Outcome       string   `json:"outcome"`
	ExitCode      string   `json:"exit_code"`
	TextExample   string   `json:"text_example,omitempty"`
	JSONExample   string   `json:"json_example,omitempty"`
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
				TextExample:   "✓ Worktree already exists: $WORKTREE_ROOT/<repo>/feature-branch\nwt navigating to: $WORKTREE_ROOT/<repo>/feature-branch",
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
				JSONExample: `{"ok":true,"command":"wt checkout","data":{"status":"exists","branch":"feature-branch","path":"$WORKTREE_ROOT/<repo>/feature-branch","navigate_to":"$WORKTREE_ROOT/<repo>/feature-branch"}}`,
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
				TextExample:   "✓ Worktree created at: $WORKTREE_ROOT/<repo>/my-feature\nwt navigating to: $WORKTREE_ROOT/<repo>/my-feature\nPath outcomes by strategy (static):\n  global: $WORKTREE_ROOT/<repo>/my-feature\n  sibling-repo: <repo-main-parent>/<repo>-my-feature\n  parent-branches: <repo-main-parent>/my-feature\n  parent-worktrees: <repo-main-parent>/<repo>.worktrees/my-feature\n  custom pattern: $WORKTREE_ROOT/custom/<repo>/my-feature",
				PathExample:   "global: $WORKTREE_ROOT/<repo>/my-feature\nsibling-repo: <repo-main-parent>/<repo>-my-feature\nparent-branches: <repo-main-parent>/my-feature\nparent-worktrees: <repo-main-parent>/<repo>.worktrees/my-feature\ncustom pattern: $WORKTREE_ROOT/custom/<repo>/my-feature",
				PathBasis:     "Static placeholders for one branch name across strategies. <repo-main-parent> is the parent directory that contains the main checkout at <repo-main-parent>/<repo>.",
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
				JSONExample:  `{"ok":true,"command":"wt create","data":{"status":"created","branch":"my-feature","base":"main","strategy":"global","pattern":"{.worktreeRoot}/{.repo.Name}/{.branch}","path":"$WORKTREE_ROOT/<repo>/my-feature","navigate_to":"$WORKTREE_ROOT/<repo>/my-feature","path_basis":"<repo-main-parent> is the parent directory containing <repo-main-parent>/<repo>","path_outcomes_by_strategy":{"global":"$WORKTREE_ROOT/<repo>/my-feature","sibling-repo":"<repo-main-parent>/<repo>-my-feature","parent-branches":"<repo-main-parent>/my-feature","parent-worktrees":"<repo-main-parent>/<repo>.worktrees/my-feature","custom pattern":"$WORKTREE_ROOT/custom/<repo>/my-feature"}}}`,
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
				TextExample:   "✓ PR #123 (pr-123) checked out at: $WORKTREE_ROOT/<repo>/pr-123\nwt navigating to: $WORKTREE_ROOT/<repo>/pr-123",
				Preconditions: []string{"gh CLI installed and authenticated for repo access."},
				FailureModes:  []string{"PR not found or inaccessible.", "Network/auth issues with GitHub."},
				FollowUp:      []string{"wt list", "wt remove pr-123"},
			},
			{
				Command:      "wt --format json pr 123",
				Purpose:      "Machine-readable PR checkout for tooling.",
				Outcome:      "JSON envelope with status, id, branch, path, navigate_to.",
				ExitCode:     "0 on success; non-zero on failure.",
				JSONExample:  `{"ok":true,"command":"wt pr","data":{"status":"created","id":"123","kind":"pr","branch":"pr-123","path":"$WORKTREE_ROOT/<repo>/pr-123","navigate_to":"$WORKTREE_ROOT/<repo>/pr-123"}}`,
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
				TextExample:   "✓ MR #123 (mr-123) checked out at: $WORKTREE_ROOT/<repo>/mr-123\nwt navigating to: $WORKTREE_ROOT/<repo>/mr-123",
				Preconditions: []string{"glab CLI installed and authenticated."},
				FailureModes:  []string{"MR not found or inaccessible.", "Network/auth issues with GitLab."},
				FollowUp:      []string{"wt list", "wt remove mr-123"},
			},
			{
				Command:      "wt --format json mr 123",
				Purpose:      "Machine-readable MR checkout.",
				Outcome:      "JSON envelope with status, id, branch, path, and navigate_to.",
				ExitCode:     "0 on success; non-zero on failure.",
				JSONExample:  `{"ok":true,"command":"wt mr","data":{"status":"created","id":"123","kind":"mr","branch":"mr-123","path":"$WORKTREE_ROOT/<repo>/mr-123","navigate_to":"$WORKTREE_ROOT/<repo>/mr-123"}}`,
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
				TextExample:  "$WORKTREE_ROOT/<repo>                                        a1b2c3d [main]\n$WORKTREE_ROOT/<repo>/feature-login                          d4e5f6a [feature-login]",
				FollowUp:     []string{"wt remove <branch>", "wt cleanup"},
				FailureModes: []string{"Non-git directory: command fails."},
			},
			{
				Command:     "wt --format json list",
				Purpose:     "Structured worktree inventory for scripts and assistants.",
				Outcome:     "JSON envelope containing data.worktrees parsed from git porcelain output.",
				ExitCode:    "0 on success.",
				JSONExample: `{"ok":true,"command":"wt list","data":{"worktrees":[{"path":"$WORKTREE_ROOT/<repo>","branch":"main","head":"a1b2c3d"},{"path":"$WORKTREE_ROOT/<repo>/feature-login","branch":"feature-login","head":"d4e5f6a"}]}}`,
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
				TextExample:   "✓ Removed worktree: $WORKTREE_ROOT/<repo>/old-branch\nwt navigating to: <main-worktree-path>\nPath outcomes by strategy (static):\n  global: $WORKTREE_ROOT/<repo>/old-branch -> (removed)\n  sibling-repo: <repo-main-parent>/<repo>-old-branch -> (removed)\n  parent-branches: <repo-main-parent>/old-branch -> (removed)\n  parent-worktrees: <repo-main-parent>/<repo>.worktrees/old-branch -> (removed)\n  custom pattern: $WORKTREE_ROOT/custom/<repo>/old-branch -> (removed)",
				PathExample:   "global: $WORKTREE_ROOT/<repo>/old-branch -> (removed)\nsibling-repo: <repo-main-parent>/<repo>-old-branch -> (removed)\nparent-branches: <repo-main-parent>/old-branch -> (removed)\nparent-worktrees: <repo-main-parent>/<repo>.worktrees/old-branch -> (removed)\ncustom pattern: $WORKTREE_ROOT/custom/<repo>/old-branch -> (removed)",
				PathBasis:     "Static placeholders for one branch name across strategies. <repo-main-parent> is the parent directory that contains the main checkout at <repo-main-parent>/<repo>.",
				Preconditions: []string{"Target branch currently has a worktree."},
				FailureModes:  []string{"Dirty worktree requires --force.", "No matching worktree for branch."},
				FollowUp:      []string{"wt list"},
			},
			{
				Command:      "wt --format json remove old-branch",
				Purpose:      "Machine-readable removal flow.",
				Outcome:      "JSON envelope with removed path and navigate_to target.",
				ExitCode:     "0 on success; non-zero on failure.",
				JSONExample:  `{"ok":true,"command":"wt remove","data":{"status":"removed","branch":"old-branch","strategy":"global","path":"$WORKTREE_ROOT/<repo>/old-branch","navigate_to":"<main-worktree-path>","path_basis":"<repo-main-parent> is the parent directory containing <repo-main-parent>/<repo>","path_outcomes_by_strategy":{"global":"$WORKTREE_ROOT/<repo>/old-branch","sibling-repo":"<repo-main-parent>/<repo>-old-branch","parent-branches":"<repo-main-parent>/old-branch","parent-worktrees":"<repo-main-parent>/<repo>.worktrees/old-branch","custom pattern":"$WORKTREE_ROOT/custom/<repo>/old-branch"}}}`,
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
				TextExample:  "Would remove 1 worktree(s) for merged branches:\n  old-feature: $WORKTREE_ROOT/<repo>/old-feature",
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
				JSONExample:  `{"ok":true,"command":"wt cleanup","data":{"dry_run":false,"base":"main","removed":1,"skipped":0}}`,
				FailureModes: []string{"In JSON mode, cleanup requires --force or --dry-run."},
			},
		},
	},
	"examples": {
		Name:        "examples",
		Description: "Show detailed command examples and outcomes",
		Examples: []usageExample{
			{
				Command:      "wt examples",
				Purpose:      "Inspect the full examples catalog for all commands.",
				Outcome:      "Prints all example topics with outcomes, text/json samples, and operational notes.",
				ExitCode:     "0 on success.",
				TextExample:  "wt examples\n\ncheckout: Checkout an existing branch in a worktree\n  wt checkout feature-branch\n...\nversion: Show wt version",
				FailureModes: []string{"This command takes no arguments; `wt examples <topic>` fails."},
				FollowUp:     []string{"wt --format json examples", "wt <command> --help"},
			},
			{
				Command:     "wt --format json examples",
				Purpose:     "Retrieve the full examples catalog as structured data.",
				Outcome:     "JSON envelope with catalog_scope, notes, and topic entries.",
				ExitCode:    "0 on success.",
				JSONExample: `{"ok":true,"command":"wt examples","data":{"catalog_scope":"full","topics":[{"name":"create","description":"Create a new branch in a worktree"}]}}`,
				SideEffects: []string{"No auto-navigation marker in JSON mode."},
			},
		},
	},
	"init": {
		Name:        "init",
		Description: "Initialize shell integration",
		Examples: []usageExample{
			{
				Command:       "wt init --dry-run",
				Purpose:       "Preview shell profile changes before writing anything.",
				Outcome:       "Shows what would be added/updated in the detected shell profile.",
				ExitCode:      "0 on success; non-zero if shell/config path detection fails.",
				TextExample:   "Would append to ~/.bashrc:\n\n# >>> wt initialize >>>\neval \"$(wt shellenv)\"\n# <<< wt initialize <<<",
				Preconditions: []string{"Run in an interactive environment where shell/profile can be detected."},
				FailureModes:  []string{"Unsupported shell argument.", "PowerShell integration requested on non-Windows host."},
				FollowUp:      []string{"wt init", "wt init --uninstall"},
			},
			{
				Command:     "wt --format json init --dry-run",
				Purpose:     "Machine-readable dry-run result for shell integration setup.",
				Outcome:     "JSON envelope describing detected shell, config path, and action.",
				ExitCode:    "0 on success; non-zero with JSON error envelope on failure.",
				JSONExample: `{"ok":true,"command":"wt init","data":{"status":"planned","shell":"bash","config_path":"~/.bashrc","dry_run":true,"operation":"install"}}`,
				Notes:       []string{"In JSON mode, use explicit shell argument in automation for deterministic behavior."},
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
				TextExample:   "Migrating worktree: $WORKTREE_ROOT_OLD/<repo>/<branch>\n  -> $WORKTREE_ROOT_NEW/<repo>/<branch>\nPath outcomes by strategy switch (static):\n  global -> sibling-repo: $WORKTREE_ROOT_OLD/<repo>/<branch> -> <repo-main-parent>/<repo>-<branch>\n  global -> parent-branches: $WORKTREE_ROOT_OLD/<repo>/<branch> -> <repo-main-parent>/<branch>\n  global -> parent-worktrees: $WORKTREE_ROOT_OLD/<repo>/<branch> -> <repo-main-parent>/<repo>.worktrees/<branch>\n  sibling-repo -> global: <repo-main-parent>/<repo>-<branch> -> $WORKTREE_ROOT_NEW/<repo>/<branch>\n  global -> custom pattern: $WORKTREE_ROOT_OLD/<repo>/<branch> -> $WORKTREE_ROOT_NEW/custom/<repo>/<branch>\nMigration complete.",
				PathExample:   "global -> sibling-repo: $WORKTREE_ROOT_OLD/<repo>/<branch> -> <repo-main-parent>/<repo>-<branch>\nglobal -> parent-branches: $WORKTREE_ROOT_OLD/<repo>/<branch> -> <repo-main-parent>/<branch>\nglobal -> parent-worktrees: $WORKTREE_ROOT_OLD/<repo>/<branch> -> <repo-main-parent>/<repo>.worktrees/<branch>\nsibling-repo -> global: <repo-main-parent>/<repo>-<branch> -> $WORKTREE_ROOT_NEW/<repo>/<branch>\nglobal -> custom pattern: $WORKTREE_ROOT_OLD/<repo>/<branch> -> $WORKTREE_ROOT_NEW/custom/<repo>/<branch>",
				PathBasis:     "Static source/destination placeholders compare strategy switches for the same branch. <repo-main-parent> is the parent directory that contains the main checkout at <repo-main-parent>/<repo>.",
				Preconditions: []string{"Set desired strategy/pattern first (wt config show / env vars)."},
				FailureModes:  []string{"Target path already exists and is non-empty.", "Filesystem move/rename failures."},
				FollowUp:      []string{"wt list", "wt info"},
			},
			{
				Command:     "wt migrate --force",
				Purpose:     "Allow replacement of non-empty targets during migration.",
				Outcome:     "Worktrees are migrated even when targets already contain files.",
				ExitCode:    "0 on success; non-zero on unrecoverable failures.",
				TextExample: "Migrating worktree with --force: $WORKTREE_ROOT_OLD/<repo>/<branch>\n  -> $WORKTREE_ROOT_NEW/<repo>/<branch>\nMigration complete.",
				SideEffects: []string{"May overwrite data at target worktree locations."},
				Notes:       []string{"Use with caution; verify destination layout before running."},
			},
			{
				Command:     "wt --format json migrate --force",
				Purpose:     "Track migration results in automation without shell parsing.",
				Outcome:     "JSON envelope reports total, migrated, skipped, and failures.",
				ExitCode:    "0 when migration completes; non-zero on fatal errors.",
				JSONExample: `{"ok":true,"command":"wt migrate","data":{"force":true,"total":4,"migrated":4,"skipped":0,"failed":0,"results":[{"branch":"feature-a","from":"$WORKTREE_ROOT_OLD/<repo>/feature-a","to":"$WORKTREE_ROOT_NEW/<repo>/feature-a","status":"moved","primary":false}]}}`,
				SideEffects: []string{"No auto-navigation marker in stdout when using JSON mode."},
				FailureModes: []string{
					"Target path conflicts may still be reported as skipped/failed entries.",
				},
			},
		},
	},
	"prune": {
		Name:        "prune",
		Description: "Remove stale worktree administrative files",
		Examples: []usageExample{
			{
				Command:     "wt prune",
				Purpose:     "Clean stale git worktree metadata entries.",
				Outcome:     "Prunes stale administrative records from git worktree metadata.",
				ExitCode:    "0 on success; non-zero on git errors.",
				TextExample: "✓ Pruned stale worktree administrative files",
				FollowUp:    []string{"wt list"},
			},
			{
				Command:     "wt --format json prune",
				Purpose:     "Use prune in automation without text parsing.",
				Outcome:     "JSON envelope confirms prune status.",
				ExitCode:    "0 on success; non-zero on failure.",
				JSONExample: `{"ok":true,"command":"wt prune","data":{"status":"pruned"}}`,
			},
		},
	},
	"shellenv": {
		Name:        "shellenv",
		Description: "Output shell wrapper for auto-navigation and completion",
		Examples: []usageExample{
			{
				Command:     "wt shellenv",
				Purpose:     "Print shell integration script to source in your shell profile.",
				Outcome:     "Outputs shell function and completion definitions for your OS/shell family.",
				ExitCode:    "0 on success.",
				TextExample: "wt() {\n    # wrapper omitted\n}\n# completion definitions...",
				Notes:       []string{"Source output in your shell profile; do not parse as structured JSON."},
			},
			{
				Command:     "wt --format json shellenv",
				Purpose:     "Programmatically detect shellenv behavior in automation.",
				Outcome:     "JSON envelope with note indicating to run shellenv without JSON for script output.",
				ExitCode:    "0 on success.",
				JSONExample: `{"ok":true,"command":"wt shellenv","data":{"note":"shellenv outputs shell script text; run without --format json to source it"}}`,
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
				TextExample: "Config: ~/.config/wt/config.toml (found)\nStrategy: global\nPattern: {.worktreeRoot}/{.repo.Name}/{.branch}\nRoot: $WORKTREE_ROOT",
				SideEffects: []string{"Read-only command."},
			},
			{
				Command:     "wt --format json info",
				Purpose:     "Get structured config metadata for automation.",
				Outcome:     "JSON envelope with config, strategies, pattern variables, and hooks.",
				ExitCode:    "0 on success.",
				JSONExample: `{"ok":true,"command":"wt info","data":{"config":{"strategy":"global","pattern":"{.worktreeRoot}/{.repo.Name}/{.branch}","root":"$WORKTREE_ROOT"}}}`,
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
				TextExample:  "Config file: ~/.config/wt/config.toml (found)\nEffective configuration:\n  root = \"$WORKTREE_ROOT\" (env WORKTREE_ROOT)",
				SideEffects:  []string{"Read-only command."},
				FailureModes: []string{"Malformed config file may produce parse errors."},
			},
			{
				Command:      "wt config init",
				Purpose:      "Create default config file.",
				Outcome:      "Config file is created unless it already exists.",
				ExitCode:     "0 on success; non-zero if config exists and --force was not provided.",
				TextExample:  "Created config file: ~/.config/wt/config.toml",
				FailureModes: []string{"Permission issues when writing config path."},
				FollowUp:     []string{"wt config show", "wt info"},
			},
			{
				Command:     "wt --format json config show",
				Purpose:     "Structured config introspection for tools.",
				Outcome:     "JSON envelope with effective values and source information.",
				ExitCode:    "0 on success.",
				JSONExample: `{"ok":true,"command":"wt config show","data":{"effective":{"root":{"value":"$WORKTREE_ROOT","source":"env WORKTREE_ROOT"}}}}`,
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
				TextExample: "wt version 0.1.0",
				SideEffects: []string{"Read-only command."},
			},
			{
				Command:     "wt --format json version",
				Purpose:     "Expose version in machine-readable envelope.",
				Outcome:     "JSON with data.version.",
				ExitCode:    "0 on success.",
				JSONExample: `{"ok":true,"command":"wt version","data":{"version":"0.1.0"}}`,
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
			printListSection("preconditions", ex.Preconditions)
			if ex.PathExample != "" {
				fmt.Printf("    path example: %s\n", ex.PathExample)
			}
			if ex.PathBasis != "" {
				fmt.Printf("    path basis: %s\n", ex.PathBasis)
			}
			if ex.TextExample != "" {
				fmt.Println("    text example:")
				for _, line := range splitLines(ex.TextExample) {
					fmt.Printf("      %s\n", line)
				}
			}
			if ex.JSONExample != "" {
				fmt.Printf("    json example: %s\n", ex.JSONExample)
			}
			printListSection("common failures", ex.FailureModes)
			printListSection("follow-up", ex.FollowUp)
			printListSection("notes", ex.Notes)
			fmt.Println()
		}
	}
}

func splitLines(input string) []string {
	if input == "" {
		return nil
	}
	return strings.Split(input, "\n")
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

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config represents the wt configuration file structure.
type Config struct {
	Root        string `toml:"root"`
	RepoRoot    string `toml:"repo_root"`
	Strategy    string `toml:"strategy"`
	Pattern     string `toml:"pattern"`
	Separator   string `toml:"separator"`
	Hooks       Hooks  `toml:"hooks"`
	HooksPolicy string `toml:"hooks_policy"`
	RepoPattern string `toml:"repo_pattern"`

	// Context holds path-matched rules supplying environment variables to
	// pattern rendering. Decoded from the user's config file only — see
	// contextRules in context.go.
	Context []ContextRule `toml:"context"`
}

// Hooks holds pre/post command hook commands.
type Hooks struct {
	PreCreate    []string `toml:"pre_create"`
	PostCreate   []string `toml:"post_create"`
	PreCheckout  []string `toml:"pre_checkout"`
	PostCheckout []string `toml:"post_checkout"`
	PreRemove    []string `toml:"pre_remove"`
	PostRemove   []string `toml:"post_remove"`
	PrePR        []string `toml:"pre_pr"`
	PostPR       []string `toml:"post_pr"`
	PreMR        []string `toml:"pre_mr"`
	PostMR       []string `toml:"post_mr"`
	PreClone     []string `toml:"pre_clone"`
	PostClone    []string `toml:"post_clone"`
}

// configSource tracks where each config value came from.
type configSource struct {
	Root        string
	RepoRoot    string
	Strategy    string
	Pattern     string
	Separator   string
	RepoPattern string
	HooksPolicy string
}

// configFilePath is the resolved path to the config file (set during loading).
var configFilePath string

// configFileFound indicates whether the config file was found during loading.
var configFileFound bool

// configRepoPath is the resolved path to the repo-level .wt.toml (set during loading).
var configRepoPath string

// configRepoFound indicates whether a repo-level .wt.toml was found during loading.
var configRepoFound bool

// configRepoKey is the repository identity a trust record is pinned to,
// resolved while the .wt.toml is being read.
//
// Resolved here rather than when a hook is about to run because by then the
// working directory may be gone: `wt remove` fires post_remove hooks after git
// has deleted the worktree the user was standing in, and `git rev-parse` from a
// deleted directory answers nothing. Config load is the last moment the answer
// is reliably available.
var configRepoKey string

// configRepoSHA is the sha256 of the repo-level .wt.toml bytes that were
// actually decoded, and is what hook approvals are pinned to.
var configRepoSHA string

// configSources tracks the origin of each resolved value.
var configSources configSource

// worktreeHooks holds the effective (merged) hook configuration.
var worktreeHooks Hooks

// repoConfigHooks holds the hooks the repo-level .wt.toml supplied, kept
// separate so they can be shown for approval before anything runs.
var repoConfigHooks Hooks

// hookSources records which config layer supplied each hook event's commands.
// The merge below replaces a whole event at a time, so one source per event is
// exact — and it is what tells runHooks whether it is looking at commands the
// user wrote or commands a repository shipped.
var hookSources = map[string]string{}

// hooksPolicy is the configured hook approval policy (see hookPolicy* in
// hooks.go). Deliberately loaded from the user's config file only.
var hooksPolicy string

// Clone placement configuration, loaded by loadWorktreeConfig.
var (
	// reposRoot is the base directory for canonical clones (wt clone).
	reposRoot string
	// repoPattern is the placement layout for canonical clones.
	repoPattern string
)

// configFlag is the --config flag value (set by cobra).
var configFlag string

// gitRepoRootFn returns the git repository root directory.
// It is a variable so tests can inject a fake implementation.
var gitRepoRootFn = defaultGitRepoRoot

// defaultGitRepoRoot uses git rev-parse to find the repo root.
func defaultGitRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// gitConfigScope identifies which git config file a value came from.
type gitConfigScope string

const (
	gitScopeGlobal gitConfigScope = "global"
	gitScopeLocal  gitConfigScope = "local"
)

// gitConfigEntry is one key/value record as git reported it.
//
// A slice rather than a map because two properties are needed that a map cannot
// carry: repeated keys (a context rule sets one `env` key once per variable,
// with `--add`) and the order git listed them in, which is the order rules are
// composed in.
type gitConfigEntry struct {
	// Key is the full dotted name. git lowercases the section and the variable
	// name but preserves the case of any subsection, and that is kept as-is
	// here so a rule's name survives; lowercase for lookups.
	Key   string
	Value string
}

// gitConfigFn reads the wt.* keys from a single git config scope.
// It is a variable so tests can inject a fake implementation.
var gitConfigFn = defaultGitConfig

// defaultGitConfig reads wt.* keys from the given git config scope, in the
// order git reports them.
//
// A missing scope is not an error. `git config --get-regexp` exits 1 when no key
// matches, and exits with other non-zero codes when the scope is unavailable
// (for example --local outside a repository). Both mean "nothing configured
// here", so any error yields no entries rather than failing the command. This
// matches how a malformed config file is treated on the TOML path, which is
// also skipped rather than reported.
func defaultGitConfig(scope gitConfigScope) []gitConfigEntry {
	// --null makes the output unambiguous: records are NUL-separated and the
	// key is separated from its value by a newline, so values containing
	// spaces or newlines survive intact. A key set with no value at all has no
	// newline in its record, which is how it is told apart from an empty one.
	cmd := exec.Command("git", "config", "--"+string(scope), "--null", "--get-regexp", `^wt\.`)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var entries []gitConfigEntry
	for _, record := range strings.Split(string(output), "\x00") {
		if record == "" {
			continue
		}
		key, value, hasValue := strings.Cut(record, "\n")
		if !hasValue {
			// `[wt]\n\tseparator` with no "=": git reports it as valueless.
			// There is no setting for which that is meaningful, so it is
			// treated as unset rather than as an empty string.
			continue
		}
		entries = append(entries, gitConfigEntry{Key: key, Value: value})
	}
	return entries
}

// gitConfigValues collapses entries to one value per key, for the scalar
// settings.
//
// The last value wins: git lists multi-valued keys in lowest-to-highest
// precedence order, and for a scalar the final one is the effective value.
// Keys are lowercased so lookups can be written in one spelling.
func gitConfigValues(entries []gitConfigEntry) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		values[strings.ToLower(entry.Key)] = entry.Value
	}
	return values
}

// applyGitConfig applies wt.* scalar settings from one git config scope.
// Hooks are intentionally not read from git config: representing lists is
// possible via multi-valued keys, but the merge semantics (replace vs append,
// and how to clear inherited hooks) are not settled for the TOML sources
// either, so git config stays scalar-only.
func applyGitConfig(entries []gitConfigEntry, sourceLabel string) {
	values := gitConfigValues(entries)
	if len(values) == 0 {
		return
	}

	if v := strings.TrimSpace(values["wt.root"]); v != "" {
		worktreeRoot = expandHome(v)
		configSources.Root = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.repo_root"]); v != "" {
		reposRoot = expandHome(v)
		configSources.RepoRoot = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.strategy"]); v != "" {
		worktreeStrategy = strings.ToLower(v)
		configSources.Strategy = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.pattern"]); v != "" {
		worktreePattern = v
		configSources.Pattern = sourceLabel
	}
	// separator is applied even when empty: an empty separator is a meaningful
	// value, matching how WORKTREE_SEPARATOR is handled.
	if v, ok := values["wt.separator"]; ok {
		worktreeSeparator = v
		configSources.Separator = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.repo_pattern"]); v != "" {
		repoPattern = v
		configSources.RepoPattern = sourceLabel
	}
}

// defaultConfigTemplate is the content written by `wt config init`.
const defaultConfigTemplate = `# wt configuration file
# See: https://github.com/timvw/wt#configuration

# Root directory for worktrees (default: ~/dev/worktrees)
# root = "~/dev/worktrees"

# Base directory for canonical clones created by 'wt clone' (default: ~/dev/repos).
# repo_root = "~/dev/repos"

# Worktree placement strategy
# Options: global, sibling-repo, parent-branches, parent-worktrees,
#          parent-dotdir, inside-dotdir, custom
# strategy = "global"

# Custom pattern (used when strategy = "custom", or to override any strategy's default)
# Available variables: {.worktreeRoot}, {.repo.Name}, {.repo.Main},
#                      {.repo.Owner}, {.repo.Host}, {.branch},
#                      {.env.VARNAME} (access environment variables, e.g. {.env.USER})
# pattern = "{.worktreeRoot}/{.repo.Name}/{.branch}"

# Separator replaces "/" and "\" in template value variables ({.branch}, {.repo.Owner}, {.env.*})
# Default is "/" (no transformation — slashes create subdirectories).
# Set to "-" or "_" for flat paths (e.g. feat/foo -> feat-foo).
# Does NOT affect path variables ({.repo.Main}, {.worktreeRoot}).
# separator = "/"

# Example: group worktrees by a FEATURE environment variable
# strategy = "custom"
# pattern = "{.worktreeRoot}/{.env.FEATURE}/{.repo.Name}"

# Placement layout for repos cloned by 'wt clone'. Variables: {.repoRoot},
# {.repo.Host}, {.repo.Owner}, {.repo.Name}, {.branch}, {.env.VARNAME}.
# {.branch} is the remote's default branch (via git ls-remote, fallback "main"),
# which makes the clone a valid main-worktree slot for sibling strategies.
# repo_pattern = "{.repoRoot}/{.repo.Host}/{.repo.Owner}/{.repo.Name}/{.branch}"

# Example: add your own grouping level (a "category", a client, a year) with an
# environment variable, then override it per invocation:
#   WT_CATEGORY=work wt clone o/r
# Give it a default in your shell rc: referencing an unset variable is an error,
# while an empty value just collapses the segment away.
# repo_pattern = "{.repoRoot}/{.env.WT_CATEGORY}/{.repo.Owner}/{.repo.Name}/{.branch}"

# Set variables for a whole tree of repositories, instead of exporting them per
# command. Each rule matches a path prefix; every matching rule applies, and
# later rules override earlier ones per variable. An exported variable always
# wins over a rule.
#
# Matched against the repository's main checkout for worktree commands, and
# against the current directory for 'wt clone' (no repo exists yet).
#
# The same rules can live in ~/.gitconfig instead (this file wins on conflict):
#   git config --global wt.context.work.whenpath "~/dev/repos/work"
#   git config --global --add wt.context.work.env "WT_CATEGORY=work"
#
# [[context]]
# when_path = "~/dev/repos/work"
# env = { WT_CATEGORY = "work" }
#
# [[context]]
# when_path = "~/dev/repos/personal"
# env = { WT_CATEGORY = "personal" }

# Hooks — run commands before/after wt operations
# Available env vars in hooks: $WT_PATH, $WT_BRANCH, $WT_MAIN,
#                              $WT_REPO_NAME, $WT_REPO_HOST, $WT_REPO_OWNER
# Pre-hooks abort on failure; post-hooks warn only.
# Set WT_HOOKS_DISABLED=1 to skip all hooks.
#
# Hooks from a repository's committed .wt.toml are not run until you approve
# them ('wt trust'). Hooks from THIS file are yours, so they run as-is unless
# you ask for more:
#   prompt-untrusted  (default) approve hooks that came from a repo's .wt.toml
#   prompt-all        confirm every hook batch, whatever supplied it
#   trusted-only      never prompt; skip anything not already approved
#   off               run no hooks at all
# hooks_policy = "prompt-untrusted"
# NOTE: Always quote path variables ("$WT_PATH") to handle spaces in paths.
#
# [hooks]
# post_create = ["test -f \"$WT_MAIN/.env\" && cp \"$WT_MAIN/.env\" \"$WT_PATH/.env\" || true"]
# post_checkout = ["cd \"$WT_PATH\" && npm install"]
# pre_remove = ["echo \"Removing $WT_PATH\""]
# post_clone = ["cd \"$WT_PATH\" && git status"]
`

// configDir returns the directory where wt config files are stored.
func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "wt")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "wt")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wt")
}

// resolveConfigPath determines which config file to use.
// Priority: --config flag > WT_CONFIG env var > default location.
func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envPath := os.Getenv("WT_CONFIG"); envPath != "" {
		return envPath
	}
	return filepath.Join(configDir(), "config.toml")
}

// loadWorktreeConfig loads configuration from git config, files and environment
// variables.
//
// Precedence, highest first:
//
//	env vars > local git config > repo .wt.toml > config file > global git
//	config > defaults
//
// --config and WT_CONFIG are not a precedence layer of their own: they select
// which TOML file is loaded, and its values still sit below .wt.toml and env.
func loadWorktreeConfig() {
	// 1. Start with defaults
	home, _ := os.UserHomeDir()
	defaultRoot := filepath.Join(home, "dev", "worktrees")

	worktreeRoot = defaultRoot
	reposRoot = filepath.Join(home, "dev", "repos")
	worktreeStrategy = "global"
	worktreePattern = ""
	worktreeSeparator = "/"

	configSources = configSource{
		Root:        "default",
		RepoRoot:    "default",
		Strategy:    "default",
		Pattern:     "default",
		Separator:   "default",
		RepoPattern: "default",
		HooksPolicy: "default",
	}

	// Reset hooks
	worktreeHooks = Hooks{}
	repoConfigHooks = Hooks{}
	hookSources = map[string]string{}
	hooksPolicy = ""
	contextRules = nil

	repoPattern = defaultRepoPattern

	// 2. Global git config (~/.gitconfig) — the broadest fallback, below wt's
	// own config file. Read once and used twice: the scalar settings, and the
	// wt.context.* rules, which no other git scope may supply.
	globalGit := gitConfigFn(gitScopeGlobal)
	applyGitConfig(globalGit, "git config (global)")
	contextRules = contextRulesFromGitConfig(globalGit)

	// 3. Load config file
	configFilePath = resolveConfigPath(configFlag)
	configFileFound = false

	if _, err := os.Stat(configFilePath); err == nil {
		configFileFound = true
		var cfg Config
		if _, err := toml.DecodeFile(configFilePath, &cfg); err == nil {
			if cfg.Root != "" {
				worktreeRoot = expandHome(cfg.Root)
				configSources.Root = "config file"
			}
			if cfg.RepoRoot != "" {
				reposRoot = expandHome(cfg.RepoRoot)
				configSources.RepoRoot = "config file"
			}
			if cfg.Strategy != "" {
				worktreeStrategy = strings.ToLower(strings.TrimSpace(cfg.Strategy))
				configSources.Strategy = "config file"
			}
			if cfg.Pattern != "" {
				worktreePattern = strings.TrimSpace(cfg.Pattern)
				configSources.Pattern = "config file"
			}
			if cfg.Separator != "" {
				worktreeSeparator = cfg.Separator
				configSources.Separator = "config file"
			}
			if cfg.RepoPattern != "" {
				repoPattern = strings.TrimSpace(cfg.RepoPattern)
				configSources.RepoPattern = "config file"
			}
			worktreeHooks = cfg.Hooks
			for _, event := range hookEvents {
				if len(hooksOf(cfg.Hooks, event)) > 0 {
					hookSources[event] = hookSourceConfigFile
				}
			}
			if cfg.HooksPolicy != "" {
				hooksPolicy = strings.ToLower(strings.TrimSpace(cfg.HooksPolicy))
				configSources.HooksPolicy = "config file"
			}
			// Appended after the git config rules rather than replacing them,
			// so the two sources compose the way rules within one source
			// already do: every matching rule applies, later definitions win
			// per variable. Because these come last, the config file wins
			// wherever both sources set the same variable for the same path —
			// which is the documented precedence — while a git config rule for
			// some unrelated tree keeps working instead of silently vanishing
			// the moment a [[context]] block is added to this file.
			contextRules = append(contextRules, cfg.Context...)
		}
	}

	// 4. Load repo-level .wt.toml (overrides global config, but NOT root and
	//    NOT the clone settings). `wt clone` acquires a repository unrelated to
	//    whichever one you happen to be standing in, so letting that repo's
	//    .wt.toml redirect the destination or run clone hooks would be wrong.
	configRepoPath = ""
	configRepoFound = false
	configRepoSHA = ""
	configRepoKey = ""

	if repoRoot, err := gitRepoRootFn(); err == nil {
		repoConfigPath := filepath.Join(repoRoot, ".wt.toml")
		configRepoPath = repoConfigPath
		// Read once and hash those exact bytes, rather than hashing the path
		// again when the hooks are about to run. Approval has to be pinned to
		// the commands actually decoded here: re-reading later would leave a
		// window in which the file is swapped, and wt would check one file's
		// hash while running another file's commands.
		if data, err := os.ReadFile(repoConfigPath); err == nil {
			configRepoFound = true
			configRepoSHA = hashBytes(data)
			if key, err := repoTrustKeyFn(); err == nil {
				configRepoKey = key
			}
			var repoCfg Config
			if _, err := toml.Decode(string(data), &repoCfg); err == nil {
				// root, repo_root and repo_pattern are intentionally NOT loaded
				// from repo config
				if repoCfg.Strategy != "" {
					worktreeStrategy = strings.ToLower(strings.TrimSpace(repoCfg.Strategy))
					configSources.Strategy = "repo config"
				}
				if repoCfg.Pattern != "" {
					worktreePattern = strings.TrimSpace(repoCfg.Pattern)
					configSources.Pattern = "repo config"
				}
				if repoCfg.Separator != "" {
					worktreeSeparator = repoCfg.Separator
					configSources.Separator = "repo config"
				}
				// hooks_policy is deliberately NOT read from repo config: a
				// repository choosing how closely wt scrutinises that same
				// repository's hooks would defeat the point.

				// Merge hooks: repo hooks override per-hook type, unset hooks keep
				// global values. Which layer won is recorded in hookSources, because
				// commands that arrived here from a committed file need the user's
				// approval before they run (see approveHooks).
				repoConfigHooks = repoCfg.Hooks
				// pre_clone/post_clone are deliberately not merged from repo
				// config: clone targets a different repository than this one.
				repoConfigHooks.PreClone = nil
				repoConfigHooks.PostClone = nil
				for _, event := range hookEvents {
					cmds := hooksOf(repoConfigHooks, event)
					if len(cmds) == 0 {
						continue
					}
					setHooks(&worktreeHooks, event, cmds)
					hookSources[event] = hookSourceRepoConfig
				}
			}
		}
	}

	// 5. Local git config (.git/config) — personal, machine-local settings that
	// are not committed, so they outrank the committed .wt.toml. Unlike
	// .wt.toml, this may set root: it is user-controlled local state rather
	// than project policy arriving via a pull request.
	//
	// Linked worktrees share the main repository's .git/config, so a value set
	// once in the main checkout applies from every worktree of that repo.
	//
	// wt.context.* is deliberately not read here — see contextRulesFromGitConfig.
	applyGitConfig(gitConfigFn(gitScopeLocal), "git config (local)")

	// 6. Environment variables override every file-based source
	if v := os.Getenv("WORKTREE_ROOT"); v != "" {
		worktreeRoot = v
		configSources.Root = "env: WORKTREE_ROOT"
	}
	if v := os.Getenv("WT_REPO_ROOT"); v != "" {
		reposRoot = expandHome(v)
		configSources.RepoRoot = "env: WT_REPO_ROOT"
	}
	if v := os.Getenv("WT_REPO_PATTERN"); v != "" {
		repoPattern = strings.TrimSpace(v)
		configSources.RepoPattern = "env: WT_REPO_PATTERN"
	}
	if v := os.Getenv("WORKTREE_STRATEGY"); v != "" {
		worktreeStrategy = strings.ToLower(strings.TrimSpace(v))
		configSources.Strategy = "env: WORKTREE_STRATEGY"
	}
	if v := os.Getenv("WORKTREE_PATTERN"); v != "" {
		worktreePattern = strings.TrimSpace(v)
		configSources.Pattern = "env: WORKTREE_PATTERN"
	}
	if v, ok := os.LookupEnv("WORKTREE_SEPARATOR"); ok {
		worktreeSeparator = v
		configSources.Separator = "env: WORKTREE_SEPARATOR"
	}
}

// expandHome replaces a leading ~ with the user's home directory
// and expands environment variables ($VAR, ${VAR}, and %VAR% on Windows).
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		path = filepath.Join(home, path[1:])
	}
	expanded := os.ExpandEnv(path)
	if runtime.GOOS == "windows" {
		expanded = expandWindowsEnv(expanded)
	}
	return expanded
}

// expandWindowsEnv expands %VAR% style environment variables.
func expandWindowsEnv(path string) string {
	for {
		start := strings.Index(path, "%")
		if start == -1 {
			break
		}
		end := strings.Index(path[start+1:], "%")
		if end == -1 {
			break
		}
		end += start + 1
		varName := path[start+1 : end]
		if val, ok := os.LookupEnv(varName); ok {
			path = path[:start] + val + path[end+1:]
		} else {
			path = path[:start] + path[end+1:]
		}
	}
	return path
}

// writeDefaultConfig creates a default config file at the given path.
func writeDefaultConfig(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config file already exists: %s (use --force to overwrite)", path)
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(defaultConfigTemplate), 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

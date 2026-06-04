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
	Root      string `toml:"root"`
	Strategy  string `toml:"strategy"`
	Pattern   string `toml:"pattern"`
	Separator string `toml:"separator"`
	Hooks     Hooks  `toml:"hooks"`
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
}

// configSource tracks where each config value came from.
type configSource struct {
	Root      string
	Strategy  string
	Pattern   string
	Separator string
}

// configFilePath is the resolved path to the config file (set during loading).
var configFilePath string

// configFileFound indicates whether the config file was found during loading.
var configFileFound bool

// configRepoPath is the resolved path to the repo-level .wt.toml (set during loading).
var configRepoPath string

// configRepoFound indicates whether a repo-level .wt.toml was found during loading.
var configRepoFound bool

// configSources tracks the origin of each resolved value.
var configSources configSource

// worktreeHooks holds the loaded hook configuration.
var worktreeHooks Hooks

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

// defaultConfigTemplate is the content written by `wt config init`.
const defaultConfigTemplate = `# wt configuration file
# See: https://github.com/timvw/wt#configuration

# Root directory for worktrees (default: ~/dev/worktrees)
# root = "~/dev/worktrees"

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

# Hooks — run commands before/after wt operations
# Available env vars in hooks: $WT_PATH, $WT_BRANCH, $WT_MAIN,
#                              $WT_REPO_NAME, $WT_REPO_HOST, $WT_REPO_OWNER
# Pre-hooks abort on failure; post-hooks warn only.
# Set WT_HOOKS_DISABLED=1 to skip all hooks.
# NOTE: Always quote path variables ("$WT_PATH") to handle spaces in paths.
#
# [hooks]
# post_create = ["test -f \"$WT_MAIN/.env\" && cp \"$WT_MAIN/.env\" \"$WT_PATH/.env\" || true"]
# post_checkout = ["cd \"$WT_PATH\" && npm install"]
# pre_remove = ["echo \"Removing $WT_PATH\""]
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

// loadWorktreeConfig loads configuration from file and environment variables.
// Precedence: env vars > config file > defaults.
func loadWorktreeConfig() {
	// 1. Start with defaults
	home, _ := os.UserHomeDir()
	defaultRoot := filepath.Join(home, "dev", "worktrees")

	worktreeRoot = defaultRoot
	worktreeStrategy = "global"
	worktreePattern = ""
	worktreeSeparator = "/"

	configSources = configSource{
		Root:      "default",
		Strategy:  "default",
		Pattern:   "default",
		Separator: "default",
	}

	// Reset hooks
	worktreeHooks = Hooks{}

	// 2. Load config file
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
			worktreeHooks = cfg.Hooks
		}
	}

	// 3. Load repo-level .wt.toml (overrides global config, but NOT root)
	configRepoPath = ""
	configRepoFound = false

	if repoRoot, err := gitRepoRootFn(); err == nil {
		repoConfigPath := filepath.Join(repoRoot, ".wt.toml")
		configRepoPath = repoConfigPath
		if _, err := os.Stat(repoConfigPath); err == nil {
			configRepoFound = true
			var repoCfg Config
			if _, err := toml.DecodeFile(repoConfigPath, &repoCfg); err == nil {
				// root is intentionally NOT loaded from repo config
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
				// Merge hooks: repo hooks override per-hook type, unset hooks keep global values
				if len(repoCfg.Hooks.PreCreate) > 0 {
					worktreeHooks.PreCreate = repoCfg.Hooks.PreCreate
				}
				if len(repoCfg.Hooks.PostCreate) > 0 {
					worktreeHooks.PostCreate = repoCfg.Hooks.PostCreate
				}
				if len(repoCfg.Hooks.PreCheckout) > 0 {
					worktreeHooks.PreCheckout = repoCfg.Hooks.PreCheckout
				}
				if len(repoCfg.Hooks.PostCheckout) > 0 {
					worktreeHooks.PostCheckout = repoCfg.Hooks.PostCheckout
				}
				if len(repoCfg.Hooks.PreRemove) > 0 {
					worktreeHooks.PreRemove = repoCfg.Hooks.PreRemove
				}
				if len(repoCfg.Hooks.PostRemove) > 0 {
					worktreeHooks.PostRemove = repoCfg.Hooks.PostRemove
				}
				if len(repoCfg.Hooks.PrePR) > 0 {
					worktreeHooks.PrePR = repoCfg.Hooks.PrePR
				}
				if len(repoCfg.Hooks.PostPR) > 0 {
					worktreeHooks.PostPR = repoCfg.Hooks.PostPR
				}
				if len(repoCfg.Hooks.PreMR) > 0 {
					worktreeHooks.PreMR = repoCfg.Hooks.PreMR
				}
				if len(repoCfg.Hooks.PostMR) > 0 {
					worktreeHooks.PostMR = repoCfg.Hooks.PostMR
				}
			}
		}
	}

	// 4. Environment variables override config file and repo config
	if v := os.Getenv("WORKTREE_ROOT"); v != "" {
		worktreeRoot = v
		configSources.Root = "env: WORKTREE_ROOT"
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

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config represents the wt configuration file structure.
type Config struct {
	Root     string `toml:"root"`
	Strategy string `toml:"strategy"`
	Pattern  string `toml:"pattern"`
}

// configSource tracks where each config value came from.
type configSource struct {
	Root     string
	Strategy string
	Pattern  string
}

// configFilePath is the resolved path to the config file (set during loading).
var configFilePath string

// configFileFound indicates whether the config file was found during loading.
var configFileFound bool

// configSources tracks the origin of each resolved value.
var configSources configSource

// configFlag is the --config flag value (set by cobra).
var configFlag string

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
#                      {.repo.Owner}, {.repo.Host}, {.branch}, {.branchSafe},
#                      {.env.VARNAME} (access environment variables, e.g. {.env.USER})
# pattern = "{.worktreeRoot}/{.repo.Name}/{.branch}"

# Example: group worktrees by a FEATURE environment variable
# strategy = "custom"
# pattern = "{.worktreeRoot}/{.env.FEATURE}/{.repo.Name}"
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

	configSources = configSource{
		Root:     "default",
		Strategy: "default",
		Pattern:  "default",
	}

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
		}
	}

	// 3. Environment variables override config file
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
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
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

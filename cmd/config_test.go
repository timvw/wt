package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestConfigDir(t *testing.T) {
	// Save and restore XDG_CONFIG_HOME
	origXDG := os.Getenv("XDG_CONFIG_HOME")
	t.Cleanup(func() {
		os.Setenv("XDG_CONFIG_HOME", origXDG)
	})

	t.Run("uses XDG_CONFIG_HOME when set", func(t *testing.T) {
		os.Setenv("XDG_CONFIG_HOME", "/custom/config")
		got := configDir()
		want := filepath.Join("/custom/config", "wt")
		if got != want {
			t.Errorf("configDir() = %q, want %q", got, want)
		}
	})

	t.Run("uses default when XDG_CONFIG_HOME is empty", func(t *testing.T) {
		os.Setenv("XDG_CONFIG_HOME", "")
		got := configDir()
		home, _ := os.UserHomeDir()
		want := filepath.Join(home, ".config", "wt")
		if runtime.GOOS == "windows" {
			if appdata := os.Getenv("APPDATA"); appdata != "" {
				want = filepath.Join(appdata, "wt")
			}
		}
		if got != want {
			t.Errorf("configDir() = %q, want %q", got, want)
		}
	})
}

func TestResolveConfigPath(t *testing.T) {
	origEnv := os.Getenv("WT_CONFIG")
	t.Cleanup(func() {
		os.Setenv("WT_CONFIG", origEnv)
	})

	t.Run("flag takes highest priority", func(t *testing.T) {
		os.Setenv("WT_CONFIG", "/env/config.toml")
		got := resolveConfigPath("/flag/config.toml")
		if got != "/flag/config.toml" {
			t.Errorf("resolveConfigPath() = %q, want /flag/config.toml", got)
		}
	})

	t.Run("env var used when no flag", func(t *testing.T) {
		os.Setenv("WT_CONFIG", "/env/config.toml")
		got := resolveConfigPath("")
		if got != "/env/config.toml" {
			t.Errorf("resolveConfigPath() = %q, want /env/config.toml", got)
		}
	})

	t.Run("default path when no flag and no env", func(t *testing.T) {
		os.Setenv("WT_CONFIG", "")
		got := resolveConfigPath("")
		if !strings.HasSuffix(got, filepath.Join("wt", "config.toml")) {
			t.Errorf("resolveConfigPath() = %q, want suffix wt/config.toml", got)
		}
	})
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot determine home directory")
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "expands ~/path",
			path: "~/projects/worktrees",
			want: filepath.Join(home, "projects", "worktrees"),
		},
		{
			name: "expands ~ alone",
			path: "~",
			want: home,
		},
		{
			name: "does not expand ~user",
			path: "~otheruser/path",
			want: "~otheruser/path",
		},
		{
			name: "absolute path unchanged",
			path: "/absolute/path",
			want: "/absolute/path",
		},
		{
			name: "relative path unchanged",
			path: "relative/path",
			want: "relative/path",
		},
		{
			name: "empty string unchanged",
			path: "",
			want: "",
		},
		{
			name: "expands ~\\ backslash path",
			path: `~\projects\worktrees`,
			want: filepath.Join(home, `\projects\worktrees`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHome(tt.path)
			if got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestExpandHomeEnvVars(t *testing.T) {
	t.Setenv("WT_TEST_DIR", "/custom/path")
	t.Setenv("WT_TEST_TEAM", "myteam")

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "expands $VAR",
			path: "$WT_TEST_DIR/worktrees",
			want: "/custom/path/worktrees",
		},
		{
			name: "expands ${VAR}",
			path: "${WT_TEST_DIR}/worktrees",
			want: "/custom/path/worktrees",
		},
		{
			name: "tilde with env var in rest of path",
			path: "~/$WT_TEST_TEAM/worktrees",
			want: func() string {
				home, _ := os.UserHomeDir()
				return filepath.Join(home, "myteam", "worktrees")
			}(),
		},
		{
			name: "tilde alone still works",
			path: "~/worktrees",
			want: func() string {
				home, _ := os.UserHomeDir()
				return filepath.Join(home, "worktrees")
			}(),
		},
		{
			name: "unset var expands to empty",
			path: "$WT_NONEXISTENT_VAR/worktrees",
			want: "/worktrees",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHome(tt.path)
			if got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestExpandWindowsEnv(t *testing.T) {
	t.Setenv("WT_WIN_TEST", `C:\Users\TestUser`)

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "expands %VAR%",
			path: `%WT_WIN_TEST%\worktrees`,
			want: `C:\Users\TestUser\worktrees`,
		},
		{
			name: "expands multiple %VAR%",
			path: `%WT_WIN_TEST%\%WT_WIN_TEST%`,
			want: `C:\Users\TestUser\C:\Users\TestUser`,
		},
		{
			name: "unset %VAR% expands to empty",
			path: `%WT_NONEXISTENT%\worktrees`,
			want: `\worktrees`,
		},
		{
			name: "no percent signs unchanged",
			path: `C:\plain\path`,
			want: `C:\plain\path`,
		},
		{
			name: "single percent sign unchanged",
			path: `50% done`,
			want: `50% done`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandWindowsEnv(tt.path)
			if got != tt.want {
				t.Errorf("expandWindowsEnv(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestWriteDefaultConfig(t *testing.T) {
	t.Run("creates config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "wt", "config.toml")

		err := writeDefaultConfig(path, false)
		if err != nil {
			t.Fatalf("writeDefaultConfig() error = %v", err)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("failed to read config file: %v", err)
		}

		if !strings.Contains(string(content), "wt configuration file") {
			t.Error("config file does not contain expected header")
		}
		if !strings.Contains(string(content), "strategy") {
			t.Error("config file does not contain strategy setting")
		}
	})

	t.Run("fails if file exists without force", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "config.toml")

		if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
			t.Fatal(err)
		}

		err := writeDefaultConfig(path, false)
		if err == nil {
			t.Fatal("expected error when file exists, got nil")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("expected 'already exists' in error, got: %v", err)
		}
	})

	t.Run("overwrites with force", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "config.toml")

		if err := os.WriteFile(path, []byte("existing"), 0o644); err != nil {
			t.Fatal(err)
		}

		err := writeDefaultConfig(path, true)
		if err != nil {
			t.Fatalf("writeDefaultConfig(force=true) error = %v", err)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "wt configuration file") {
			t.Error("config file not overwritten with default content")
		}
	})

	t.Run("creates parent directories", func(t *testing.T) {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "deep", "nested", "config.toml")

		err := writeDefaultConfig(path, false)
		if err != nil {
			t.Fatalf("writeDefaultConfig() error = %v", err)
		}

		if _, err := os.Stat(path); err != nil {
			t.Fatalf("config file not created at %s", path)
		}
	})
}

func TestLoadWorktreeConfig(t *testing.T) {
	// Save original state
	origRoot := worktreeRoot
	origStrategy := worktreeStrategy
	origPattern := worktreePattern
	origConfigFlag := configFlag
	origConfigFilePath := configFilePath
	origConfigFileFound := configFileFound
	origConfigSources := configSources
	origReposRoot := reposRoot
	origRepoPattern := repoPattern
	origGitConfigFn := gitConfigFn
	origEnvRoot := os.Getenv("WORKTREE_ROOT")
	origEnvStrategy := os.Getenv("WORKTREE_STRATEGY")
	origEnvPattern := os.Getenv("WORKTREE_PATTERN")
	origEnvConfig := os.Getenv("WT_CONFIG")

	t.Cleanup(func() {
		worktreeRoot = origRoot
		worktreeStrategy = origStrategy
		worktreePattern = origPattern
		configFlag = origConfigFlag
		configFilePath = origConfigFilePath
		configFileFound = origConfigFileFound
		configSources = origConfigSources
		reposRoot = origReposRoot
		repoPattern = origRepoPattern
		gitConfigFn = origGitConfigFn
		os.Setenv("WORKTREE_ROOT", origEnvRoot)
		os.Setenv("WORKTREE_STRATEGY", origEnvStrategy)
		os.Setenv("WORKTREE_PATTERN", origEnvPattern)
		os.Setenv("WT_CONFIG", origEnvConfig)
	})

	// Isolate from the developer's real git config: a contributor with wt.*
	// keys in ~/.gitconfig would otherwise fail these tests.
	gitConfigFn = func(gitConfigScope) []gitConfigEntry { return nil }

	t.Run("loads defaults when no config file", func(t *testing.T) {
		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Setenv("WT_CONFIG", "/nonexistent/config.toml")
		configFlag = ""

		loadWorktreeConfig()

		home, _ := os.UserHomeDir()
		expectedRoot := filepath.Join(home, "dev", "worktrees")
		if worktreeRoot != expectedRoot {
			t.Errorf("worktreeRoot = %q, want %q", worktreeRoot, expectedRoot)
		}
		if worktreeStrategy != "global" {
			t.Errorf("worktreeStrategy = %q, want global", worktreeStrategy)
		}
		if worktreePattern != "" {
			t.Errorf("worktreePattern = %q, want empty", worktreePattern)
		}
		if configFileFound {
			t.Error("configFileFound should be false for nonexistent file")
		}
		if configSources.Root != "default" {
			t.Errorf("configSources.Root = %q, want default", configSources.Root)
		}
	})

	t.Run("loads repo_root and repo_pattern", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.toml")
		cfgContent := `repo_root = "/custom/base"
repo_pattern = "{.repoRoot}/{.repo.Name}"
`
		if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
			t.Fatal(err)
		}

		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Setenv("WT_CONFIG", cfgPath)
		configFlag = ""

		loadWorktreeConfig()

		if reposRoot != "/custom/base" {
			t.Errorf("reposRoot = %q, want /custom/base", reposRoot)
		}
		if configSources.RepoRoot != "config file" {
			t.Errorf("configSources.RepoRoot = %q, want 'config file'", configSources.RepoRoot)
		}
		if repoPattern != "{.repoRoot}/{.repo.Name}" {
			t.Errorf("repoPattern = %q, want custom pattern", repoPattern)
		}
		if configSources.RepoPattern != "config file" {
			t.Errorf("configSources.RepoPattern = %q, want 'config file'", configSources.RepoPattern)
		}
	})

	t.Run("defaults repo_root and repo_pattern when unset", func(t *testing.T) {
		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Setenv("WT_CONFIG", "/nonexistent/config.toml")
		configFlag = ""

		loadWorktreeConfig()

		home, _ := os.UserHomeDir()
		if want := filepath.Join(home, "dev", "repos"); reposRoot != want {
			t.Errorf("reposRoot = %q, want %q", reposRoot, want)
		}
		if repoPattern != defaultRepoPattern {
			t.Errorf("repoPattern = %q, want %q", repoPattern, defaultRepoPattern)
		}
	})

	t.Run("loads from config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.toml")
		cfgContent := `root = "/custom/worktrees"
strategy = "sibling-repo"
pattern = "{.worktreeRoot}/custom/{.branch}"
`
		if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
			t.Fatal(err)
		}

		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Setenv("WT_CONFIG", cfgPath)
		configFlag = ""

		loadWorktreeConfig()

		if worktreeRoot != "/custom/worktrees" {
			t.Errorf("worktreeRoot = %q, want /custom/worktrees", worktreeRoot)
		}
		if worktreeStrategy != "sibling-repo" {
			t.Errorf("worktreeStrategy = %q, want sibling-repo", worktreeStrategy)
		}
		if worktreePattern != "{.worktreeRoot}/custom/{.branch}" {
			t.Errorf("worktreePattern = %q, want {.worktreeRoot}/custom/{.branch}", worktreePattern)
		}
		if !configFileFound {
			t.Error("configFileFound should be true")
		}
		if configSources.Root != "config file" {
			t.Errorf("configSources.Root = %q, want 'config file'", configSources.Root)
		}
		if configSources.Strategy != "config file" {
			t.Errorf("configSources.Strategy = %q, want 'config file'", configSources.Strategy)
		}
	})

	t.Run("env vars override config file", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.toml")
		cfgContent := `root = "/config/worktrees"
strategy = "global"
`
		if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
			t.Fatal(err)
		}

		os.Setenv("WORKTREE_ROOT", "/env/worktrees")
		os.Setenv("WORKTREE_STRATEGY", "parent-branches")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Setenv("WT_CONFIG", cfgPath)
		configFlag = ""

		loadWorktreeConfig()

		if worktreeRoot != "/env/worktrees" {
			t.Errorf("worktreeRoot = %q, want /env/worktrees", worktreeRoot)
		}
		if worktreeStrategy != "parent-branches" {
			t.Errorf("worktreeStrategy = %q, want parent-branches", worktreeStrategy)
		}
		if configSources.Root != "env: WORKTREE_ROOT" {
			t.Errorf("configSources.Root = %q, want 'env: WORKTREE_ROOT'", configSources.Root)
		}
		if configSources.Strategy != "env: WORKTREE_STRATEGY" {
			t.Errorf("configSources.Strategy = %q, want 'env: WORKTREE_STRATEGY'", configSources.Strategy)
		}
	})

	t.Run("config flag overrides WT_CONFIG env", func(t *testing.T) {
		tmpDir := t.TempDir()

		envCfg := filepath.Join(tmpDir, "env-config.toml")
		if err := os.WriteFile(envCfg, []byte(`strategy = "global"`), 0o644); err != nil {
			t.Fatal(err)
		}

		flagCfg := filepath.Join(tmpDir, "flag-config.toml")
		if err := os.WriteFile(flagCfg, []byte(`strategy = "sibling-repo"`), 0o644); err != nil {
			t.Fatal(err)
		}

		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Setenv("WT_CONFIG", envCfg)
		configFlag = flagCfg

		loadWorktreeConfig()

		if worktreeStrategy != "sibling-repo" {
			t.Errorf("worktreeStrategy = %q, want sibling-repo (from flag config)", worktreeStrategy)
		}
		if configFilePath != flagCfg {
			t.Errorf("configFilePath = %q, want %q", configFilePath, flagCfg)
		}
	})

	t.Run("config file with tilde expansion in root", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.toml")
		cfgContent := `root = "~/my-worktrees"
`
		if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
			t.Fatal(err)
		}

		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Setenv("WT_CONFIG", cfgPath)
		configFlag = ""

		loadWorktreeConfig()

		home, _ := os.UserHomeDir()
		expected := filepath.Join(home, "my-worktrees")
		if worktreeRoot != expected {
			t.Errorf("worktreeRoot = %q, want %q", worktreeRoot, expected)
		}
	})

	t.Run("strategy is lowercased and trimmed", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.toml")
		cfgContent := `strategy = "  Sibling-Repo  "
`
		if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
			t.Fatal(err)
		}

		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Setenv("WT_CONFIG", cfgPath)
		configFlag = ""

		loadWorktreeConfig()

		if worktreeStrategy != "sibling-repo" {
			t.Errorf("worktreeStrategy = %q, want sibling-repo", worktreeStrategy)
		}
	})
}

func TestLoadWorktreeConfigRepoConfig(t *testing.T) {
	// Save original state
	origRoot := worktreeRoot
	origStrategy := worktreeStrategy
	origPattern := worktreePattern
	origSeparator := worktreeSeparator
	origConfigFlag := configFlag
	origConfigFilePath := configFilePath
	origConfigFileFound := configFileFound
	origConfigSources := configSources
	origHooks := worktreeHooks
	origRepoPath := configRepoPath
	origRepoFound := configRepoFound
	origGitRepoRootFn := gitRepoRootFn
	origGitConfigFn := gitConfigFn
	origReposRoot := reposRoot
	origRepoPattern := repoPattern
	origEnvRoot := os.Getenv("WORKTREE_ROOT")
	origEnvStrategy := os.Getenv("WORKTREE_STRATEGY")
	origEnvPattern := os.Getenv("WORKTREE_PATTERN")
	origEnvSeparator, envSepSet := os.LookupEnv("WORKTREE_SEPARATOR")
	origEnvConfig := os.Getenv("WT_CONFIG")

	t.Cleanup(func() {
		worktreeRoot = origRoot
		worktreeStrategy = origStrategy
		worktreePattern = origPattern
		worktreeSeparator = origSeparator
		configFlag = origConfigFlag
		configFilePath = origConfigFilePath
		configFileFound = origConfigFileFound
		configSources = origConfigSources
		worktreeHooks = origHooks
		configRepoPath = origRepoPath
		configRepoFound = origRepoFound
		gitRepoRootFn = origGitRepoRootFn
		gitConfigFn = origGitConfigFn
		reposRoot = origReposRoot
		repoPattern = origRepoPattern
		os.Setenv("WORKTREE_ROOT", origEnvRoot)
		os.Setenv("WORKTREE_STRATEGY", origEnvStrategy)
		os.Setenv("WORKTREE_PATTERN", origEnvPattern)
		if envSepSet {
			os.Setenv("WORKTREE_SEPARATOR", origEnvSeparator)
		} else {
			os.Unsetenv("WORKTREE_SEPARATOR")
		}
		os.Setenv("WT_CONFIG", origEnvConfig)
	})

	clearEnv := func() {
		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Unsetenv("WORKTREE_SEPARATOR")
		os.Setenv("WT_CONFIG", "/nonexistent/config.toml")
		configFlag = ""
		// Isolate from the developer's real git config.
		gitConfigFn = func(gitConfigScope) []gitConfigEntry { return nil }
	}

	t.Run("repo config overrides global config", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create global config
		globalCfg := filepath.Join(tmpDir, "global.toml")
		if err := os.WriteFile(globalCfg, []byte(`strategy = "global"
separator = "/"
`), 0o644); err != nil {
			t.Fatal(err)
		}

		// Create repo config
		repoDir := filepath.Join(tmpDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(`strategy = "sibling-repo"
separator = "-"
`), 0o644); err != nil {
			t.Fatal(err)
		}

		clearEnv()
		os.Setenv("WT_CONFIG", globalCfg)
		gitRepoRootFn = func() (string, error) { return repoDir, nil }

		loadWorktreeConfig()

		if worktreeStrategy != "sibling-repo" {
			t.Errorf("worktreeStrategy = %q, want sibling-repo", worktreeStrategy)
		}
		if worktreeSeparator != "-" {
			t.Errorf("worktreeSeparator = %q, want \"-\"", worktreeSeparator)
		}
		if configSources.Strategy != "repo config" {
			t.Errorf("configSources.Strategy = %q, want 'repo config'", configSources.Strategy)
		}
		if configSources.Separator != "repo config" {
			t.Errorf("configSources.Separator = %q, want 'repo config'", configSources.Separator)
		}
		if !configRepoFound {
			t.Error("configRepoFound should be true")
		}
		if configRepoPath != filepath.Join(repoDir, ".wt.toml") {
			t.Errorf("configRepoPath = %q, want %q", configRepoPath, filepath.Join(repoDir, ".wt.toml"))
		}
	})

	t.Run("env vars override repo config", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create repo config
		repoDir := filepath.Join(tmpDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(`strategy = "sibling-repo"
separator = "-"
`), 0o644); err != nil {
			t.Fatal(err)
		}

		clearEnv()
		os.Setenv("WORKTREE_STRATEGY", "parent-branches")
		gitRepoRootFn = func() (string, error) { return repoDir, nil }

		loadWorktreeConfig()

		if worktreeStrategy != "parent-branches" {
			t.Errorf("worktreeStrategy = %q, want parent-branches", worktreeStrategy)
		}
		if configSources.Strategy != "env: WORKTREE_STRATEGY" {
			t.Errorf("configSources.Strategy = %q, want 'env: WORKTREE_STRATEGY'", configSources.Strategy)
		}
		// separator should still come from repo config
		if worktreeSeparator != "-" {
			t.Errorf("worktreeSeparator = %q, want \"-\"", worktreeSeparator)
		}
	})

	t.Run("missing repo config is fine", func(t *testing.T) {
		tmpDir := t.TempDir()
		repoDir := filepath.Join(tmpDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}

		clearEnv()
		gitRepoRootFn = func() (string, error) { return repoDir, nil }

		loadWorktreeConfig()

		if configRepoFound {
			t.Error("configRepoFound should be false when .wt.toml doesn't exist")
		}
		if worktreeStrategy != "global" {
			t.Errorf("worktreeStrategy = %q, want global (default)", worktreeStrategy)
		}
	})

	t.Run("not in git repo is fine", func(t *testing.T) {
		clearEnv()
		gitRepoRootFn = func() (string, error) { return "", fmt.Errorf("not in a git repo") }

		loadWorktreeConfig()

		if configRepoFound {
			t.Error("configRepoFound should be false when not in a git repo")
		}
		if configRepoPath != "" {
			t.Errorf("configRepoPath = %q, want empty", configRepoPath)
		}
	})

	t.Run("repo config does not support root", func(t *testing.T) {
		tmpDir := t.TempDir()

		repoDir := filepath.Join(tmpDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(`root = "/some/path"
strategy = "sibling-repo"
`), 0o644); err != nil {
			t.Fatal(err)
		}

		clearEnv()
		gitRepoRootFn = func() (string, error) { return repoDir, nil }

		loadWorktreeConfig()

		home, _ := os.UserHomeDir()
		expectedRoot := filepath.Join(home, "dev", "worktrees")
		if worktreeRoot != expectedRoot {
			t.Errorf("worktreeRoot = %q, want %q (root should be ignored from repo config)", worktreeRoot, expectedRoot)
		}
		if configSources.Root != "default" {
			t.Errorf("configSources.Root = %q, want 'default' (root should be ignored from repo config)", configSources.Root)
		}
		// But strategy should still be loaded
		if worktreeStrategy != "sibling-repo" {
			t.Errorf("worktreeStrategy = %q, want sibling-repo", worktreeStrategy)
		}
	})

	t.Run("repo hooks override global hooks", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create global config with hooks
		globalCfg := filepath.Join(tmpDir, "global.toml")
		if err := os.WriteFile(globalCfg, []byte(`strategy = "global"

[hooks]
post_create = ["make build"]
pre_remove = ["echo removing"]
`), 0o644); err != nil {
			t.Fatal(err)
		}

		// Create repo config with hooks
		repoDir := filepath.Join(tmpDir, "repo")
		if err := os.MkdirAll(repoDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(`[hooks]
post_create = ["npm install"]
`), 0o644); err != nil {
			t.Fatal(err)
		}

		clearEnv()
		os.Setenv("WT_CONFIG", globalCfg)
		gitRepoRootFn = func() (string, error) { return repoDir, nil }

		loadWorktreeConfig()

		if len(worktreeHooks.PostCreate) != 1 || worktreeHooks.PostCreate[0] != "npm install" {
			t.Errorf("worktreeHooks.PostCreate = %v, want [npm install]", worktreeHooks.PostCreate)
		}
		// pre_remove from global should be kept since repo didn't override it
		if len(worktreeHooks.PreRemove) != 1 || worktreeHooks.PreRemove[0] != "echo removing" {
			t.Errorf("worktreeHooks.PreRemove = %v, want [echo removing] (from global config)", worktreeHooks.PreRemove)
		}
	})
}

func TestLoadWorktreeConfigGitConfig(t *testing.T) {
	// Save original state
	origRoot := worktreeRoot
	origStrategy := worktreeStrategy
	origPattern := worktreePattern
	origSeparator := worktreeSeparator
	origConfigFlag := configFlag
	origConfigFilePath := configFilePath
	origConfigFileFound := configFileFound
	origConfigSources := configSources
	origHooks := worktreeHooks
	origRepoPath := configRepoPath
	origRepoFound := configRepoFound
	origGitRepoRootFn := gitRepoRootFn
	origGitConfigFn := gitConfigFn
	origReposRoot := reposRoot
	origRepoPattern := repoPattern
	origEnvRoot := os.Getenv("WORKTREE_ROOT")
	origEnvStrategy := os.Getenv("WORKTREE_STRATEGY")
	origEnvPattern := os.Getenv("WORKTREE_PATTERN")
	origEnvSeparator, envSepSet := os.LookupEnv("WORKTREE_SEPARATOR")
	origEnvConfig := os.Getenv("WT_CONFIG")

	t.Cleanup(func() {
		worktreeRoot = origRoot
		worktreeStrategy = origStrategy
		worktreePattern = origPattern
		worktreeSeparator = origSeparator
		configFlag = origConfigFlag
		configFilePath = origConfigFilePath
		configFileFound = origConfigFileFound
		configSources = origConfigSources
		worktreeHooks = origHooks
		configRepoPath = origRepoPath
		configRepoFound = origRepoFound
		gitRepoRootFn = origGitRepoRootFn
		gitConfigFn = origGitConfigFn
		reposRoot = origReposRoot
		repoPattern = origRepoPattern
		os.Setenv("WORKTREE_ROOT", origEnvRoot)
		os.Setenv("WORKTREE_STRATEGY", origEnvStrategy)
		os.Setenv("WORKTREE_PATTERN", origEnvPattern)
		if envSepSet {
			os.Setenv("WORKTREE_SEPARATOR", origEnvSeparator)
		} else {
			os.Unsetenv("WORKTREE_SEPARATOR")
		}
		os.Setenv("WT_CONFIG", origEnvConfig)
	})

	clearEnv := func() {
		os.Setenv("WORKTREE_ROOT", "")
		os.Setenv("WORKTREE_STRATEGY", "")
		os.Setenv("WORKTREE_PATTERN", "")
		os.Unsetenv("WORKTREE_SEPARATOR")
		os.Setenv("WT_CONFIG", "/nonexistent/config.toml")
		configFlag = ""
		gitRepoRootFn = func() (string, error) { return t.TempDir(), nil }
		gitConfigFn = func(gitConfigScope) []gitConfigEntry { return nil }
	}

	// stubGitConfig serves per-scope values to the loader. The map form is fine
	// for the scalar settings these subtests cover: each key appears once, so
	// the order entries are produced in cannot change the outcome. Ordered
	// entries are exercised directly in context_test.go.
	stubGitConfig := func(global, local map[string]string) {
		entries := func(values map[string]string) []gitConfigEntry {
			var out []gitConfigEntry
			for key, value := range values {
				out = append(out, gitConfigEntry{Key: key, Value: value})
			}
			return out
		}
		gitConfigFn = func(scope gitConfigScope) []gitConfigEntry {
			switch scope {
			case gitScopeGlobal:
				return entries(global)
			case gitScopeLocal:
				return entries(local)
			}
			return nil
		}
	}

	t.Run("global git config applies over defaults", func(t *testing.T) {
		clearEnv()
		stubGitConfig(map[string]string{
			"wt.root":      "/from/global/git",
			"wt.strategy":  "sibling-repo",
			"wt.pattern":   "{.worktreeRoot}/global-git/{.branch}",
			"wt.separator": "-",
		}, nil)

		loadWorktreeConfig()

		if worktreeRoot != "/from/global/git" {
			t.Errorf("worktreeRoot = %q, want /from/global/git", worktreeRoot)
		}
		if worktreeStrategy != "sibling-repo" {
			t.Errorf("worktreeStrategy = %q, want sibling-repo", worktreeStrategy)
		}
		if worktreeSeparator != "-" {
			t.Errorf("worktreeSeparator = %q, want -", worktreeSeparator)
		}
		if configSources.Root != "git config (global)" {
			t.Errorf("configSources.Root = %q, want git config (global)", configSources.Root)
		}
	})

	t.Run("config file overrides global git config", func(t *testing.T) {
		tmpDir := t.TempDir()
		cfgPath := filepath.Join(tmpDir, "config.toml")
		if err := os.WriteFile(cfgPath, []byte(`strategy = "parent-branches"
`), 0o644); err != nil {
			t.Fatal(err)
		}

		clearEnv()
		os.Setenv("WT_CONFIG", cfgPath)
		stubGitConfig(map[string]string{"wt.strategy": "sibling-repo"}, nil)

		loadWorktreeConfig()

		if worktreeStrategy != "parent-branches" {
			t.Errorf("worktreeStrategy = %q, want parent-branches (config file wins)", worktreeStrategy)
		}
		if configSources.Strategy != "config file" {
			t.Errorf("configSources.Strategy = %q, want config file", configSources.Strategy)
		}
	})

	t.Run("local git config overrides repo .wt.toml", func(t *testing.T) {
		repoDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(`strategy = "parent-worktrees"
separator = "_"
`), 0o644); err != nil {
			t.Fatal(err)
		}

		clearEnv()
		gitRepoRootFn = func() (string, error) { return repoDir, nil }
		stubGitConfig(nil, map[string]string{"wt.strategy": "sibling-repo"})

		loadWorktreeConfig()

		if worktreeStrategy != "sibling-repo" {
			t.Errorf("worktreeStrategy = %q, want sibling-repo (local git wins over .wt.toml)", worktreeStrategy)
		}
		if configSources.Strategy != "git config (local)" {
			t.Errorf("configSources.Strategy = %q, want git config (local)", configSources.Strategy)
		}
		// .wt.toml still supplies settings local git config does not set.
		if worktreeSeparator != "_" {
			t.Errorf("worktreeSeparator = %q, want _ (from .wt.toml)", worktreeSeparator)
		}
		if configSources.Separator != "repo config" {
			t.Errorf("configSources.Separator = %q, want repo config", configSources.Separator)
		}
	})

	t.Run("local git config overrides global git config", func(t *testing.T) {
		clearEnv()
		stubGitConfig(
			map[string]string{"wt.strategy": "sibling-repo", "wt.root": "/from/global/git"},
			map[string]string{"wt.strategy": "parent-branches"},
		)

		loadWorktreeConfig()

		if worktreeStrategy != "parent-branches" {
			t.Errorf("worktreeStrategy = %q, want parent-branches", worktreeStrategy)
		}
		// global still supplies root, which local does not set
		if worktreeRoot != "/from/global/git" {
			t.Errorf("worktreeRoot = %q, want /from/global/git", worktreeRoot)
		}
		if configSources.Root != "git config (global)" {
			t.Errorf("configSources.Root = %q, want git config (global)", configSources.Root)
		}
	})

	t.Run("env vars override local git config", func(t *testing.T) {
		clearEnv()
		stubGitConfig(nil, map[string]string{"wt.strategy": "sibling-repo"})
		os.Setenv("WORKTREE_STRATEGY", "parent-branches")

		loadWorktreeConfig()

		if worktreeStrategy != "parent-branches" {
			t.Errorf("worktreeStrategy = %q, want parent-branches", worktreeStrategy)
		}
		if configSources.Strategy != "env: WORKTREE_STRATEGY" {
			t.Errorf("configSources.Strategy = %q, want env: WORKTREE_STRATEGY", configSources.Strategy)
		}
	})

	t.Run("local git config may set root unlike .wt.toml", func(t *testing.T) {
		repoDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(`root = "/ignored/from/wt/toml"
`), 0o644); err != nil {
			t.Fatal(err)
		}

		clearEnv()
		gitRepoRootFn = func() (string, error) { return repoDir, nil }
		stubGitConfig(nil, map[string]string{"wt.root": "/from/local/git"})

		loadWorktreeConfig()

		if worktreeRoot != "/from/local/git" {
			t.Errorf("worktreeRoot = %q, want /from/local/git", worktreeRoot)
		}
	})

	t.Run("no wt keys leaves other sources untouched", func(t *testing.T) {
		clearEnv()
		stubGitConfig(map[string]string{}, map[string]string{})

		loadWorktreeConfig()

		home, _ := os.UserHomeDir()
		if worktreeRoot != filepath.Join(home, "dev", "worktrees") {
			t.Errorf("worktreeRoot = %q, want default", worktreeRoot)
		}
		if configSources.Strategy != "default" {
			t.Errorf("configSources.Strategy = %q, want default", configSources.Strategy)
		}
	})

	t.Run("blank values are ignored but empty separator applies", func(t *testing.T) {
		clearEnv()
		stubGitConfig(nil, map[string]string{
			"wt.root":      "   ",
			"wt.strategy":  "",
			"wt.separator": "",
		})

		loadWorktreeConfig()

		home, _ := os.UserHomeDir()
		if worktreeRoot != filepath.Join(home, "dev", "worktrees") {
			t.Errorf("worktreeRoot = %q, want default (blank ignored)", worktreeRoot)
		}
		if worktreeStrategy != "global" {
			t.Errorf("worktreeStrategy = %q, want global (blank ignored)", worktreeStrategy)
		}
		// An explicitly empty separator is meaningful, matching WORKTREE_SEPARATOR.
		if worktreeSeparator != "" {
			t.Errorf("worktreeSeparator = %q, want empty", worktreeSeparator)
		}
		if configSources.Separator != "git config (local)" {
			t.Errorf("configSources.Separator = %q, want git config (local)", configSources.Separator)
		}
	})

	t.Run("strategy is normalised to lowercase", func(t *testing.T) {
		clearEnv()
		stubGitConfig(nil, map[string]string{"wt.strategy": "  Sibling-Repo  "})

		loadWorktreeConfig()

		if worktreeStrategy != "sibling-repo" {
			t.Errorf("worktreeStrategy = %q, want sibling-repo", worktreeStrategy)
		}
	})

	t.Run("hooks are not read from git config", func(t *testing.T) {
		clearEnv()
		stubGitConfig(nil, map[string]string{"wt.hooks.post-create": "echo nope"})

		loadWorktreeConfig()

		if len(worktreeHooks.PostCreate) != 0 {
			t.Errorf("worktreeHooks.PostCreate = %v, want empty (hooks are out of scope)", worktreeHooks.PostCreate)
		}
	})
}

func TestDefaultGitConfigParsing(t *testing.T) {
	// Exercises the real `git config` invocation against a scratch repository,
	// covering the parsing that the stubbed tests above skip.
	repoDir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init", "-q", ".")
	run("config", "--local", "wt.pattern", "{.worktreeRoot}/with spaces/{.branch}")
	run("config", "--local", "unrelated.key", "ignored")
	// Multi-valued key: git lists values in file order, and the last one is
	// what `git config --get` would resolve to.
	run("config", "--local", "wt.strategy", "parent-branches")
	run("config", "--add", "wt.strategy", "sibling-repo")
	// Keys are matched case-insensitively by git, which reports the canonical
	// lowercase name.
	run("config", "--local", "wt.SEPARATOR", "-")
	// A subsection key must not collide with the scalar settings.
	run("config", "--local", "wt.hooks.post-create", "echo hi")

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	values := gitConfigValues(defaultGitConfig(gitScopeLocal))

	// Last value wins for a multi-valued key.
	if values["wt.strategy"] != "sibling-repo" {
		t.Errorf("wt.strategy = %q, want sibling-repo (last value)", values["wt.strategy"])
	}
	// Values containing spaces must survive record splitting.
	if values["wt.pattern"] != "{.worktreeRoot}/with spaces/{.branch}" {
		t.Errorf("wt.pattern = %q, want the pattern with spaces intact", values["wt.pattern"])
	}
	// Key case is normalised.
	if values["wt.separator"] != "-" {
		t.Errorf("wt.separator = %q, want -", values["wt.separator"])
	}
	if _, ok := values["unrelated.key"]; ok {
		t.Error("unrelated.key should not be returned")
	}
	// A subsection key is returned but must not be mistaken for a scalar.
	if _, ok := values["wt.separator.extra"]; ok {
		t.Error("unexpected key wt.separator.extra")
	}
	if v := values["wt.hooks.post-create"]; v != "echo hi" {
		t.Errorf("wt.hooks.post-create = %q, want it parsed but unused", v)
	}
}

func TestDefaultGitConfigMultilineAndValuelessKeys(t *testing.T) {
	// git config values may contain newlines, and a key may be present with no
	// value at all. Both must survive parsing without corrupting neighbours.
	repoDir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init", "-q", ".")
	run("config", "--local", "wt.pattern", "line-one\nline-two")
	run("config", "--local", "wt.strategy", "sibling-repo")

	// A valueless key can only be written by editing the file directly.
	cfgPath := filepath.Join(repoDir, ".git", "config")
	existing, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, append(existing, []byte("\tseparator\n\tcopyIgnored\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	values := gitConfigValues(defaultGitConfig(gitScopeLocal))

	if values["wt.pattern"] != "line-one\nline-two" {
		t.Errorf("wt.pattern = %q, want the embedded newline preserved", values["wt.pattern"])
	}
	// The multi-line value must not swallow the key that follows it.
	if values["wt.strategy"] != "sibling-repo" {
		t.Errorf("wt.strategy = %q, want sibling-repo", values["wt.strategy"])
	}
	// A valueless key is treated as unset, not as an empty string.
	if _, ok := values["wt.separator"]; ok {
		t.Errorf("wt.separator = %q, want absent for a valueless key", values["wt.separator"])
	}
	// Except for a boolean, where valueless is git's spelling of true —
	// `git config --bool wt.copyIgnored` reads it that way, so wt has to too.
	// (git reports the name lowercased, and rejects an underscore in it.)
	v, ok := values["wt.copyignored"]
	if !ok {
		t.Fatal("wt.copyignored is absent; a valueless boolean means true")
	}
	if b, parsed := parseGitBool(v); !parsed || !b {
		t.Errorf("parseGitBool(%q) = %v, %v; want true, true", v, b, parsed)
	}
}

func TestDefaultGitConfigOutsideRepo(t *testing.T) {
	// Outside a repository, --local is an error; it must degrade to "nothing
	// configured" rather than failing the command.
	tmpDir := t.TempDir()

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	if values := defaultGitConfig(gitScopeLocal); len(values) != 0 {
		t.Errorf("defaultGitConfig(local) = %v, want empty outside a repo", values)
	}
}

func TestConfigShowPatternParityBetweenTextAndJSON_Config(t *testing.T) {
	origRoot := worktreeRoot
	origStrategy := worktreeStrategy
	origPattern := worktreePattern
	origSeparator := worktreeSeparator
	origConfigFilePath := configFilePath
	origConfigFileFound := configFileFound
	origConfigSources := configSources
	origOutputFormat := outputFormat
	origReposRoot := reposRoot
	origRepoPattern := repoPattern

	t.Cleanup(func() {
		worktreeRoot = origRoot
		worktreeStrategy = origStrategy
		worktreePattern = origPattern
		worktreeSeparator = origSeparator
		configFilePath = origConfigFilePath
		configFileFound = origConfigFileFound
		configSources = origConfigSources
		outputFormat = origOutputFormat
		reposRoot = origReposRoot
		repoPattern = origRepoPattern
	})

	runConfigShow := func(t *testing.T, format string) string {
		t.Helper()

		origStdout := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("failed to create pipe: %v", err)
		}
		os.Stdout = w
		defer func() {
			os.Stdout = origStdout
		}()

		outputFormat = format
		if err := configShowCmd.RunE(configShowCmd, nil); err != nil {
			t.Fatalf("config show failed for format %s: %v", format, err)
		}

		if err := w.Close(); err != nil {
			t.Fatalf("failed to close write pipe: %v", err)
		}

		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			t.Fatalf("failed to read command output: %v", err)
		}

		return buf.String()
	}

	tests := []struct {
		name          string
		strategy      string
		workPattern   string
		patternSource string
		expected      string
	}{
		{
			name:          "strategy default pattern",
			strategy:      "global",
			workPattern:   "",
			patternSource: "strategy default",
			expected:      "{.worktreeRoot}/{.repo.Name}/{.branch}",
		},
		{
			name:          "explicit configured pattern",
			strategy:      "global",
			workPattern:   "{.worktreeRoot}/custom/{.branch}",
			patternSource: "config file",
			expected:      "{.worktreeRoot}/custom/{.branch}",
		},
		{
			name:          "custom strategy without explicit pattern",
			strategy:      "custom",
			workPattern:   "",
			patternSource: "default",
			expected:      "(none)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktreeRoot = "/tmp/worktrees"
			worktreeStrategy = tt.strategy
			worktreePattern = tt.workPattern
			worktreeSeparator = "-"
			configFilePath = "/tmp/config.toml"
			configFileFound = true
			configSources = configSource{
				Root:        "config file",
				RepoRoot:    "default",
				Strategy:    "config file",
				Pattern:     tt.patternSource,
				Separator:   "default",
				RepoPattern: "default",
			}

			textOut := runConfigShow(t, "text")
			jsonOut := runConfigShow(t, "json")

			textPatternRe := regexp.MustCompile(`(?m)^\s*pattern\s*=\s*(.*?)\s+\(`)
			textMatch := textPatternRe.FindStringSubmatch(textOut)
			if len(textMatch) != 2 {
				t.Fatalf("failed to parse pattern from text output: %q", textOut)
			}
			textPattern := textMatch[1]

			var payload struct {
				Data struct {
					Effective struct {
						Pattern struct {
							Value string `json:"value"`
						} `json:"pattern"`
					} `json:"effective"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
				t.Fatalf("failed to parse json output: %v\noutput=%q", err, jsonOut)
			}

			if payload.Data.Effective.Pattern.Value != textPattern {
				t.Fatalf("pattern mismatch between text and json: text=%q json=%q", textPattern, payload.Data.Effective.Pattern.Value)
			}

			expectedPattern := configShowPatternValue()
			if expectedPattern != tt.expected {
				t.Fatalf("resolved test expectation mismatch: got=%q want=%q", expectedPattern, tt.expected)
			}
			if textPattern != expectedPattern {
				t.Fatalf("text output pattern should use resolved value: got=%q want=%q", textPattern, expectedPattern)
			}
			if payload.Data.Effective.Pattern.Value != expectedPattern {
				t.Fatalf("json output pattern should use resolved value: got=%q want=%q", payload.Data.Effective.Pattern.Value, expectedPattern)
			}
		})
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
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
		os.Setenv("WORKTREE_ROOT", origEnvRoot)
		os.Setenv("WORKTREE_STRATEGY", origEnvStrategy)
		os.Setenv("WORKTREE_PATTERN", origEnvPattern)
		os.Setenv("WT_CONFIG", origEnvConfig)
	})

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

func TestConfigShowPatternParityBetweenTextAndJSON_Config(t *testing.T) {
	origRoot := worktreeRoot
	origStrategy := worktreeStrategy
	origPattern := worktreePattern
	origSeparator := worktreeSeparator
	origConfigFilePath := configFilePath
	origConfigFileFound := configFileFound
	origConfigSources := configSources
	origOutputFormat := outputFormat

	t.Cleanup(func() {
		worktreeRoot = origRoot
		worktreeStrategy = origStrategy
		worktreePattern = origPattern
		worktreeSeparator = origSeparator
		configFilePath = origConfigFilePath
		configFileFound = origConfigFileFound
		configSources = origConfigSources
		outputFormat = origOutputFormat
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
				Root:      "config file",
				Strategy:  "config file",
				Pattern:   tt.patternSource,
				Separator: "default",
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

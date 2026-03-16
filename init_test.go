package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDetectShell(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		envShell string
		want     string
	}{
		{
			name: "explicit bash argument",
			args: []string{"bash"},
			want: "bash",
		},
		{
			name: "explicit zsh argument",
			args: []string{"zsh"},
			want: "zsh",
		},
		{
			name: "explicit powershell argument",
			args: []string{"powershell"},
			want: "powershell",
		},
		{
			name: "pwsh alias returns powershell",
			args: []string{"pwsh"},
			want: "powershell",
		},
		{
			name: "case insensitive",
			args: []string{"BASH"},
			want: "bash",
		},
		{
			name:     "detect from SHELL env - zsh",
			args:     []string{},
			envShell: "/bin/zsh",
			want:     "zsh",
		},
		{
			name:     "detect from SHELL env - bash",
			args:     []string{},
			envShell: "/bin/bash",
			want:     "bash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Skip Windows-specific tests on non-Windows
			if runtime.GOOS == "windows" && tt.envShell != "" {
				t.Skip("Skipping SHELL env test on Windows")
			}

			// Save and restore SHELL env var
			origShell := os.Getenv("SHELL")
			if tt.envShell != "" {
				os.Setenv("SHELL", tt.envShell)
			}
			defer os.Setenv("SHELL", origShell)

			got := detectShell(tt.args)
			if got != tt.want {
				t.Errorf("detectShell(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

func TestSupportedShells(t *testing.T) {
	// Verify all expected shells are in the map
	expected := []string{"bash", "zsh", "powershell", "pwsh"}
	for _, shell := range expected {
		if !supportedShells[shell] {
			t.Errorf("supportedShells missing %q", shell)
		}
	}

	// Verify no unexpected shells
	if len(supportedShells) != len(expected) {
		t.Errorf("supportedShells has unexpected entries: got %d, want %d", len(supportedShells), len(expected))
	}
}

func TestGetShellConfigPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home dir: %v", err)
	}

	// Ensure tests are stable even when the developer machine has ZDOTDIR set.
	origZdotdir := os.Getenv("ZDOTDIR")
	os.Setenv("ZDOTDIR", "")
	t.Cleanup(func() { os.Setenv("ZDOTDIR", origZdotdir) })

	tests := []struct {
		name  string
		shell string
		want  string
	}{
		{
			name:  "zsh config path",
			shell: "zsh",
			want:  filepath.Join(home, ".zshrc"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getShellConfigPath(tt.shell)
			if got != tt.want {
				t.Errorf("getShellConfigPath(%q) = %q, want %q", tt.shell, got, tt.want)
			}
		})
	}
}

func TestGetShellConfigPathZshRespectsZdotdir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Failed to get home dir: %v", err)
	}

	orig := os.Getenv("ZDOTDIR")
	t.Cleanup(func() { os.Setenv("ZDOTDIR", orig) })

	zdotdir := filepath.Join(home, ".config", "zsh")
	os.Setenv("ZDOTDIR", zdotdir)

	got := getShellConfigPath("zsh")
	want := filepath.Join(zdotdir, ".zshrc")
	if got != want {
		t.Fatalf("getShellConfigPath(%q) = %q, want %q", "zsh", got, want)
	}
}

func TestGetShellConfigContent(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		contains []string
	}{
		{
			name:     "bash content",
			shell:    "bash",
			contains: []string{markerStart, markerEnd, "wt shellenv"},
		},
		{
			name:     "zsh content",
			shell:    "zsh",
			contains: []string{markerStart, markerEnd, "wt shellenv"},
		},
		{
			name:     "powershell content",
			shell:    "powershell",
			contains: []string{markerStart, markerEnd, "wt shellenv", "Invoke-Expression"},
		},
		{
			name:  "unsupported shell returns empty",
			shell: "fish",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getShellConfigContent(tt.shell)
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("getShellConfigContent(%q) missing %q", tt.shell, want)
				}
			}
			if len(tt.contains) == 0 && got != "" {
				t.Errorf("getShellConfigContent(%q) = %q, want empty", tt.shell, got)
			}
		})
	}
}

func TestSuccessPrefix(t *testing.T) {
	tests := []struct {
		name    string
		envCI   string
		envTerm string
		want    string
	}{
		{
			name: "normal terminal shows checkmark",
			want: "✓",
		},
		{
			name:  "CI environment shows [ok]",
			envCI: "true",
			want:  "[ok]",
		},
		{
			name:    "dumb terminal shows [ok]",
			envTerm: "dumb",
			want:    "[ok]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore env vars
			origCI := os.Getenv("CI")
			origTerm := os.Getenv("TERM")
			defer func() {
				os.Setenv("CI", origCI)
				os.Setenv("TERM", origTerm)
			}()

			os.Unsetenv("CI")
			os.Unsetenv("TERM")
			if tt.envCI != "" {
				os.Setenv("CI", tt.envCI)
			}
			if tt.envTerm != "" {
				os.Setenv("TERM", tt.envTerm)
			}

			got := successPrefix()
			if got != tt.want {
				t.Errorf("successPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInstallAndRemoveShellConfig(t *testing.T) {
	// Create a temp directory for test files
	tmpDir, err := os.MkdirTemp("", "wt-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, ".bashrc")

	// Test install on new file
	t.Run("install on new file", func(t *testing.T) {
		err := installShellConfig(configPath, "bash", false, true)
		if err != nil {
			t.Fatalf("installShellConfig failed: %v", err)
		}

		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("Failed to read config: %v", err)
		}

		if !strings.Contains(string(content), markerStart) {
			t.Error("Config missing start marker")
		}
		if !strings.Contains(string(content), markerEnd) {
			t.Error("Config missing end marker")
		}
		if !strings.Contains(string(content), "wt shellenv") {
			t.Error("Config missing shellenv command")
		}
	})

	// Test idempotent install
	t.Run("idempotent install", func(t *testing.T) {
		err := installShellConfig(configPath, "bash", false, true)
		if err != nil {
			t.Fatalf("Second installShellConfig failed: %v", err)
		}

		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("Failed to read config: %v", err)
		}

		// Should only have one occurrence of the marker
		count := strings.Count(string(content), markerStart)
		if count != 1 {
			t.Errorf("Expected 1 occurrence of marker, got %d", count)
		}
	})

	// Test remove
	t.Run("remove config", func(t *testing.T) {
		err := removeShellConfig(configPath, "bash", false)
		if err != nil {
			t.Fatalf("removeShellConfig failed: %v", err)
		}

		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("Failed to read config: %v", err)
		}

		if strings.Contains(string(content), markerStart) {
			t.Error("Config still contains start marker after removal")
		}
		if strings.Contains(string(content), markerEnd) {
			t.Error("Config still contains end marker after removal")
		}
	})

	// Test preserves existing content
	t.Run("preserves existing content", func(t *testing.T) {
		existingContent := "# My existing config\nexport MY_VAR=hello\n"
		err := os.WriteFile(configPath, []byte(existingContent), 0644)
		if err != nil {
			t.Fatalf("Failed to write existing config: %v", err)
		}

		err = installShellConfig(configPath, "bash", false, true)
		if err != nil {
			t.Fatalf("installShellConfig failed: %v", err)
		}

		content, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("Failed to read config: %v", err)
		}

		if !strings.Contains(string(content), "MY_VAR=hello") {
			t.Error("Existing content was not preserved")
		}

		// Remove and verify existing content still present
		err = removeShellConfig(configPath, "bash", false)
		if err != nil {
			t.Fatalf("removeShellConfig failed: %v", err)
		}

		content, err = os.ReadFile(configPath)
		if err != nil {
			t.Fatalf("Failed to read config: %v", err)
		}

		if !strings.Contains(string(content), "MY_VAR=hello") {
			t.Error("Existing content was not preserved after removal")
		}
	})
}

func TestDryRun(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wt-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, ".bashrc")

	// Dry run should not create file
	t.Run("dry run does not create file", func(t *testing.T) {
		err := installShellConfig(configPath, "bash", true, true)
		if err != nil {
			t.Fatalf("installShellConfig dry run failed: %v", err)
		}

		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			t.Error("Dry run should not create file")
		}
	})

	// Create file for removal test
	t.Run("dry run does not modify file on remove", func(t *testing.T) {
		content := markerStart + "\neval \"$(wt shellenv)\"\n" + markerEnd + "\n"
		err := os.WriteFile(configPath, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to write config: %v", err)
		}

		err = removeShellConfig(configPath, "bash", true)
		if err != nil {
			t.Fatalf("removeShellConfig dry run failed: %v", err)
		}

		// File should still have markers
		readContent, _ := os.ReadFile(configPath)
		if !strings.Contains(string(readContent), markerStart) {
			t.Error("Dry run should not modify file")
		}
	})
}

func TestInitJSONOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping init JSON integration test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	homeDir := filepath.Join(tmpDir, "home")
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("failed to create HOME dir: %v", err)
	}

	cmd := exec.Command(wtBinary, "--format", "json", "init", "bash", "--dry-run")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wt init --format json --dry-run failed: %v\nOutput: %s", err, out)
	}

	var payload struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Status     string `json:"status"`
			Operation  string `json:"operation"`
			Shell      string `json:"shell"`
			ConfigPath string `json:"config_path"`
			DryRun     bool   `json:"dry_run"`
		} `json:"data"`
	}

	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("failed to parse init json output: %v\noutput=%q", err, out)
	}
	if !payload.OK {
		t.Fatalf("expected ok=true in init json output: %s", out)
	}
	if payload.Command != "wt init" {
		t.Fatalf("expected command wt init, got %q", payload.Command)
	}
	if payload.Data.Operation != "install" {
		t.Fatalf("expected operation install, got %q", payload.Data.Operation)
	}
	if payload.Data.Status != "planned" {
		t.Fatalf("expected status planned for dry-run, got %q", payload.Data.Status)
	}
	if payload.Data.Shell != "bash" {
		t.Fatalf("expected shell bash, got %q", payload.Data.Shell)
	}
	if !payload.Data.DryRun {
		t.Fatal("expected dry_run=true")
	}
	if payload.Data.ConfigPath == "" {
		t.Fatal("expected config_path to be populated")
	}
}

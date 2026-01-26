package main

import (
	"os"
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

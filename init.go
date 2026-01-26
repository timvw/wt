package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// Supported shells for init command
var supportedShells = map[string]bool{
	"bash":       true,
	"zsh":        true,
	"powershell": true,
	"pwsh":       true, // alias for powershell
}

// Init command flags
var (
	initDryRun    bool
	initUninstall bool
	initNoPrompt  bool
)

var initCmd = &cobra.Command{
	Use:   "init [shell]",
	Short: "Initialize shell integration",
	Long: `Add wt shell integration to your shell configuration.

Automatically detects your shell and updates the appropriate config file:
  - bash: ~/.bashrc
  - zsh:  ~/.zshrc
  - powershell: $PROFILE (Windows only)

The configuration is wrapped in markers so it can be safely updated or removed.

Examples:
  wt init              # Auto-detect shell and configure
  wt init bash         # Configure for bash specifically
  wt init --dry-run    # Preview changes without modifying files
  wt init --uninstall  # Remove wt configuration from shell`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		shell := detectShell(args)
		if shell == "" {
			fmt.Fprintln(os.Stderr, "Error: could not detect shell. Please specify: wt init bash|zsh|powershell")
			os.Exit(1)
		}

		// PowerShell init is only supported on Windows because wt shellenv
		// only outputs PowerShell code when running on Windows
		if shell == "powershell" && runtime.GOOS != "windows" {
			fmt.Fprintln(os.Stderr, "Error: PowerShell shell integration is only supported on Windows.")
			fmt.Fprintln(os.Stderr, "On macOS/Linux, use: wt init bash  or  wt init zsh")
			os.Exit(1)
		}

		configPath := getShellConfigPath(shell)
		if configPath == "" {
			fmt.Fprintf(os.Stderr, "Error: could not determine config file for %s\n", shell)
			os.Exit(1)
		}

		if initUninstall {
			if err := removeShellConfig(configPath, shell, initDryRun); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		if err := installShellConfig(configPath, shell, initDryRun, initNoPrompt); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

const (
	markerStart = "# >>> wt initialize >>>"
	markerEnd   = "# <<< wt initialize <<<"
)

// detectShell determines which shell to configure based on args or environment
func detectShell(args []string) string {
	// 1. Explicit argument
	if len(args) > 0 {
		shell := strings.ToLower(args[0])
		if supportedShells[shell] {
			if shell == "pwsh" {
				return "powershell"
			}
			return shell
		}
		fmt.Fprintf(os.Stderr, "Warning: unknown shell '%s', attempting auto-detection\n", args[0])
	}

	// 2. On Windows, default to PowerShell
	if runtime.GOOS == "windows" {
		return "powershell"
	}

	// 3. Check $SHELL environment variable
	shellEnv := os.Getenv("SHELL")
	if strings.Contains(shellEnv, "zsh") {
		return "zsh"
	}
	if strings.Contains(shellEnv, "bash") {
		return "bash"
	}

	// 4. Default to bash on Unix
	return "bash"
}

// getShellConfigPath returns the path to the shell configuration file
func getShellConfigPath(shell string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch shell {
	case "bash":
		// Prefer .bashrc, fall back to .bash_profile on macOS
		bashrc := filepath.Join(home, ".bashrc")
		if _, err := os.Stat(bashrc); err == nil {
			return bashrc
		}
		if runtime.GOOS == "darwin" {
			return filepath.Join(home, ".bash_profile")
		}
		return bashrc
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "powershell":
		// Check $PROFILE env var first (works for both Windows PowerShell 5.1 and PowerShell Core)
		if profile := os.Getenv("PROFILE"); profile != "" {
			return profile
		}
		if runtime.GOOS == "windows" {
			// Default to Windows PowerShell 5.1 path (more common)
			// Windows PowerShell 5.1: Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1
			// PowerShell Core 7+: Documents\PowerShell\Microsoft.PowerShell_profile.ps1
			docs := filepath.Join(home, "Documents", "WindowsPowerShell")
			if err := os.MkdirAll(docs, 0755); err == nil {
				return filepath.Join(docs, "Microsoft.PowerShell_profile.ps1")
			}
		}
		// Unix PowerShell Core
		return filepath.Join(home, ".config", "powershell", "Microsoft.PowerShell_profile.ps1")
	}
	return ""
}

// getShellConfigContent returns the shell configuration block to add
func getShellConfigContent(shell string) string {
	switch shell {
	case "bash", "zsh":
		return fmt.Sprintf(`%s
eval "$(wt shellenv)"
%s`, markerStart, markerEnd)
	case "powershell":
		return fmt.Sprintf(`%s
Invoke-Expression (& wt shellenv)
%s`, markerStart, markerEnd)
	}
	return ""
}

// successPrefix returns a checkmark or "[ok]" depending on terminal support
func successPrefix() string {
	// Check if we're in a terminal that likely supports Unicode
	// Most modern terminals do, but CI environments and some Windows consoles may not
	if os.Getenv("CI") != "" || os.Getenv("TERM") == "dumb" {
		return "[ok]"
	}
	return "✓"
}

// installShellConfig adds or updates shell configuration
func installShellConfig(configPath, shell string, dryRun, noPrompt bool) error {
	content := getShellConfigContent(shell)
	if content == "" {
		return fmt.Errorf("unsupported shell: %s", shell)
	}

	// Read existing config
	existing, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to read %s: %v", configPath, err)
	}

	existingStr := string(existing)

	// Check if already configured
	if strings.Contains(existingStr, markerStart) {
		// Update existing configuration
		startIdx := strings.Index(existingStr, markerStart)
		endIdx := strings.Index(existingStr, markerEnd)
		if endIdx > startIdx {
			endIdx += len(markerEnd)
			newContent := existingStr[:startIdx] + content + existingStr[endIdx:]

			if dryRun {
				fmt.Printf("Would update %s (already configured, updating)\n\n", configPath)
				fmt.Println("New configuration block:")
				fmt.Println(content)
				return nil
			}

			if err := os.WriteFile(configPath, []byte(newContent), 0644); err != nil {
				return fmt.Errorf("failed to write %s: %v", configPath, err)
			}
			fmt.Printf("%s Updated wt configuration in %s\n", successPrefix(), configPath)
			return nil
		}
	}

	// Append new configuration
	if dryRun {
		fmt.Printf("Would append to %s:\n\n", configPath)
		fmt.Println(content)
		fmt.Println()
		fmt.Println("To apply, run: wt init")
		return nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %v", err)
	}

	// Append to file
	f, err := os.OpenFile(configPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open %s: %v", configPath, err)
	}
	defer f.Close()

	// Add newline before if file doesn't end with one
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}

	if _, err := f.WriteString("\n" + content + "\n"); err != nil {
		return fmt.Errorf("failed to write config: %v", err)
	}

	fmt.Printf("%s Added wt shell integration to %s\n", successPrefix(), configPath)
	if !noPrompt {
		fmt.Println()
		fmt.Println("To activate, run:")
		switch shell {
		case "bash":
			fmt.Println("  source ~/.bashrc")
		case "zsh":
			fmt.Println("  source ~/.zshrc")
		case "powershell":
			fmt.Println("  . $PROFILE")
		}
		fmt.Println()
		fmt.Println("Or start a new shell session.")
	}
	return nil
}

// removeShellConfig removes the wt configuration block from shell config
func removeShellConfig(configPath, shell string, dryRun bool) error {
	existing, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		fmt.Println("No configuration found to remove.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read %s: %v", configPath, err)
	}

	existingStr := string(existing)

	if !strings.Contains(existingStr, markerStart) {
		fmt.Println("No wt configuration found in", configPath)
		return nil
	}

	startIdx := strings.Index(existingStr, markerStart)
	endIdx := strings.Index(existingStr, markerEnd)
	if endIdx <= startIdx {
		return fmt.Errorf("malformed configuration markers in %s", configPath)
	}
	endIdx += len(markerEnd)

	// Count newlines before the marker to preserve original formatting
	// We only remove the single newline we added before the marker
	before := existingStr[:startIdx]
	after := existingStr[endIdx:]

	// Remove trailing newline from before (the one we added)
	if strings.HasSuffix(before, "\n\n") {
		before = before[:len(before)-1]
	}
	// Remove leading newline from after (the one we added)
	after = strings.TrimPrefix(after, "\n")

	newContent := before + after

	if dryRun {
		fmt.Printf("Would remove wt configuration from %s\n", configPath)
		return nil
	}

	if err := os.WriteFile(configPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %v", configPath, err)
	}

	fmt.Printf("%s Removed wt configuration from %s\n", successPrefix(), configPath)
	return nil
}

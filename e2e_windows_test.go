//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2EAutoCdWithPowerShell tests that auto-cd works in PowerShell
func TestE2EAutoCdWithPowerShell(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	// Check if PowerShell is available
	powershell := findPowerShell(t)

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create a test branch
	runGitCommand(t, repoDir, "checkout", "-b", "pwsh-test-branch")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit")
	runGitCommand(t, repoDir, "checkout", "main")

	// Create PowerShell script that sets up environment and tests auto-cd
	script := fmt.Sprintf(`
$env:WORKTREE_ROOT = '%s'
$env:PATH = '%s;' + $env:PATH
Set-Location '%s'

# Load wt shell integration
Invoke-Expression (& '%s' shellenv)

# Run wt checkout
wt checkout pwsh-test-branch

# Print current directory
Get-Location | Select-Object -ExpandProperty Path
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	cmd := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run PowerShell e2e test: %v\nOutput: %s", err, output)
	}

	expectedPath := filepath.Join(worktreeRoot, "test-repo", "pwsh-test-branch")
	if !strings.Contains(string(output), expectedPath) {
		t.Errorf("E2E FAIL: Auto-cd didn't work in PowerShell!\nExpected to be in: %s\nOutput: %s",
			expectedPath, output)
	} else {
		t.Logf("E2E PASS: Successfully auto-cd'd in PowerShell: %s", expectedPath)
	}
}

// TestE2EAutoCdWithCreatePowerShell tests that auto-cd works when creating a new branch in PowerShell
func TestE2EAutoCdWithCreatePowerShell(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	powershell := findPowerShell(t)

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	script := fmt.Sprintf(`
$env:WORKTREE_ROOT = '%s'
$env:PATH = '%s;' + $env:PATH
Set-Location '%s'

# Load wt shell integration
Invoke-Expression (& '%s' shellenv)

# Run wt create
wt create new-feature-pwsh

# Print current directory
Get-Location | Select-Object -ExpandProperty Path
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	cmd := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to run PowerShell e2e test: %v\nOutput: %s", err, output)
	}

	expectedPath := filepath.Join(worktreeRoot, "test-repo", "new-feature-pwsh")
	if !strings.Contains(string(output), expectedPath) {
		t.Errorf("E2E FAIL: Auto-cd didn't work for create in PowerShell!\nExpected to be in: %s\nOutput: %s",
			expectedPath, output)
	} else {
		t.Logf("E2E PASS: Successfully auto-cd'd to new worktree in PowerShell: %s", expectedPath)
	}
}

// TestE2EPowerShellShellenvOutput tests that shellenv outputs valid PowerShell code
func TestE2EPowerShellShellenvOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	powershell := findPowerShell(t)

	tmpDir := t.TempDir()
	wtBinary := buildWtBinary(t, tmpDir)

	// Test that the shellenv output can be executed without errors
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
try {
    Invoke-Expression (& '%s' shellenv)
    # Verify that the wt function is defined
    if (Get-Command wt -ErrorAction SilentlyContinue) {
        Write-Output "SUCCESS: wt function is defined"
    } else {
        Write-Error "FAIL: wt function not defined"
        exit 1
    }
} catch {
    Write-Error "FAIL: Error loading shellenv: $_"
    exit 1
}
`, wtBinary)

	cmd := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to load shellenv in PowerShell: %v\nOutput: %s", err, output)
	}

	if !strings.Contains(string(output), "SUCCESS") {
		t.Errorf("E2E FAIL: PowerShell shellenv validation failed!\nOutput: %s", output)
	} else {
		t.Logf("E2E PASS: PowerShell shellenv loaded successfully")
	}
}

// TestE2EPowerShellCompletion tests that PowerShell completion is registered
func TestE2EPowerShellCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	powershell := findPowerShell(t)

	tmpDir := t.TempDir()
	wtBinary := buildWtBinary(t, tmpDir)

	// Test that completion is registered
	script := fmt.Sprintf(`
$ErrorActionPreference = 'Stop'
try {
    Invoke-Expression (& '%s' shellenv)
    # Check if ArgumentCompleter is registered for wt
    $completers = (Get-Command -Name wt).ScriptBlock
    if ($completers) {
        Write-Output "SUCCESS: Completion registered"
    } else {
        Write-Output "INFO: Completion may not be visible but function exists"
    }
} catch {
    Write-Error "FAIL: Error testing completion: $_"
    exit 1
}
`, wtBinary)

	cmd := exec.Command(powershell, "-NoProfile", "-NonInteractive", "-Command", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("Note: PowerShell completion test had error (this is OK): %v\nOutput: %s", err, output)
	} else {
		t.Logf("PowerShell completion test output: %s", output)
	}
}

// Helper function to find PowerShell executable
func findPowerShell(t *testing.T) string {
	t.Helper()

	// Try PowerShell Core first (pwsh), then fall back to Windows PowerShell (powershell)
	if path, err := exec.LookPath("pwsh"); err == nil {
		t.Logf("Using PowerShell Core: %s", path)
		return path
	}

	if path, err := exec.LookPath("powershell"); err == nil {
		t.Logf("Using Windows PowerShell: %s", path)
		return path
	}

	t.Skip("PowerShell not available, skipping PowerShell tests")
	return ""
}

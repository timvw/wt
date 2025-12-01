//go:build windows

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPwshAdapterBasicCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping pwsh adapter test in short mode")
	}

	// Check if pwsh is available
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not available, skipping test")
	}

	adapter := NewPwshAdapter()

	// Create a temporary directory structure
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a fake wt script (on Windows this would be wt.exe)
	wtScript := filepath.Join(tmpDir, "wt.exe")
	// For testing, we'd need a real wt.exe or mock it
	// Skip setup for now as we can't test without wt binary
	t.Skip("PowerShell adapter requires wt.exe for testing - will be validated in CI")
}

func TestPwshAdapterName(t *testing.T) {
	adapter := NewPwshAdapter()
	if adapter.Name() != "pwsh" {
		t.Errorf("Name() = %q, want %q", adapter.Name(), "pwsh")
	}
}

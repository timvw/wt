package scenarios

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/timvw/wt/e2e/harness"
)

// getShellAdapters returns the list of shell adapters to test based on E2E_SHELLS env var.
// E2E_SHELLS should be a comma-separated list (e.g., "bash,zsh").
// If not set, defaults to "bash".
// Fails the test if a configured shell is not available.
func getShellAdapters(t *testing.T) []harness.ShellAdapter {
	t.Helper()

	// Get shells from environment or default to bash
	shellsEnv := os.Getenv("E2E_SHELLS")
	if shellsEnv == "" {
		shellsEnv = "bash"
		t.Logf("E2E_SHELLS not set, defaulting to: %s", shellsEnv)
	} else {
		t.Logf("E2E_SHELLS=%s", shellsEnv)
	}

	// Parse comma-separated shell names
	shellNames := strings.Split(shellsEnv, ",")
	adapters := make([]harness.ShellAdapter, 0, len(shellNames))

	for _, name := range shellNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		// Create platform-specific adapter
		adapter := createShellAdapter(name)
		if adapter == nil {
			t.Fatalf("Unknown or unsupported shell in E2E_SHELLS: %s (Windows: bash,zsh,pwsh; Unix: bash,zsh)", name)
		}

		// Verify shell is available on this system
		if err := verifyShellAvailable(name); err != nil {
			t.Fatalf("Shell '%s' configured in E2E_SHELLS but not available: %v", name, err)
		}

		adapters = append(adapters, adapter)
	}

	if len(adapters) == 0 {
		t.Fatal("No valid shell adapters configured")
	}

	return adapters
}

// verifyShellAvailable checks if a shell executable is available in PATH
func verifyShellAvailable(shell string) error {
	_, err := exec.LookPath(shell)
	if err != nil {
		return fmt.Errorf("%s not found in PATH", shell)
	}
	return nil
}

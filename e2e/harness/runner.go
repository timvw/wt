package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Runner executes scenarios through shell adapters
type Runner struct {
	t        *testing.T
	adapter  ShellAdapter
	fixture  *Fixture
	wtBinary string
}

// NewRunner creates a new test runner
func NewRunner(t *testing.T, adapter ShellAdapter) (*Runner, error) {
	t.Helper()

	// Get wt binary path from environment or build it
	wtBinary, err := getWtBinary(t)
	if err != nil {
		return nil, fmt.Errorf("failed to get wt binary: %w", err)
	}

	// Create fixture
	fixture, err := NewFixture(t, wtBinary)
	if err != nil {
		return nil, fmt.Errorf("failed to create fixture: %w", err)
	}

	// Setup the shell adapter
	if err := adapter.Setup(wtBinary, fixture.WorktreeRoot, fixture.RepoDir); err != nil {
		return nil, fmt.Errorf("failed to setup adapter: %w", err)
	}

	return &Runner{
		t:        t,
		adapter:  adapter,
		fixture:  fixture,
		wtBinary: wtBinary,
	}, nil
}

// Run executes a scenario and reports results
func (r *Runner) Run(scenario Scenario) error {
	r.t.Helper()

	r.t.Logf("Running scenario: %s", scenario.Name)
	if scenario.Description != "" {
		r.t.Logf("  Description: %s", scenario.Description)
	}

	// Execute setup
	if scenario.Setup != nil {
		r.t.Logf("  Running setup...")
		if err := scenario.Setup(r.fixture); err != nil {
			return fmt.Errorf("setup failed: %w", err)
		}
	}

	// Execute steps
	var lastResult *Result
	for i, step := range scenario.Steps {
		r.t.Logf("  Step %d: %s %v", i+1, step.Cmd, step.Args)

		result, err := r.adapter.Execute(step.Cmd, step.Args)
		if err != nil {
			return fmt.Errorf("step %d failed: %w", i+1, err)
		}

		lastResult = result
		r.t.Logf("    Exit code: %d", result.ExitCode)
		if result.Pwd != "" {
			r.t.Logf("    Pwd: %s", result.Pwd)
		}
		if result.Stdout != "" {
			r.t.Logf("    Stdout: %s", result.Stdout)
		}
		if result.Stderr != "" {
			r.t.Logf("    Stderr: %s", result.Stderr)
		}
	}

	// Run assertions
	if lastResult != nil && len(scenario.Verify) > 0 {
		r.t.Logf("  Running %d assertions...", len(scenario.Verify))
		for i, assertion := range scenario.Verify {
			if err := assertion(lastResult, r.fixture); err != nil {
				return fmt.Errorf("assertion %d failed: %w", i+1, err)
			}
			r.t.Logf("    Assertion %d: ✓", i+1)
		}
	}

	r.t.Logf("  ✓ Scenario passed: %s", scenario.Name)
	return nil
}

// Cleanup cleans up the runner resources
func (r *Runner) Cleanup() error {
	if r.adapter != nil {
		return r.adapter.Cleanup()
	}
	return nil
}

// getWtBinary returns the path to the wt binary
// Checks WT_BINARY env var, or builds from source
func getWtBinary(t *testing.T) (string, error) {
	t.Helper()

	// Check environment variable
	if binary := os.Getenv("WT_BINARY"); binary != "" {
		// Verify it exists
		if _, err := os.Stat(binary); err == nil {
			return filepath.Abs(binary)
		}
		t.Logf("WT_BINARY set but file not found: %s", binary)
	}

	// Check if wt is in PATH
	if binary, err := lookPath("wt"); err == nil {
		return filepath.Abs(binary)
	}

	// Build from source
	t.Logf("Building wt from source...")
	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "wt")

	// Use go build
	if err := buildWt(binaryPath); err != nil {
		return "", fmt.Errorf("failed to build wt: %w", err)
	}

	return binaryPath, nil
}

// lookPath searches for an executable in PATH
func lookPath(name string) (string, error) {
	path := os.Getenv("PATH")
	if path == "" {
		return "", fmt.Errorf("PATH not set")
	}

	pathSep := ":"
	if os.PathSeparator == '\\' {
		pathSep = ";"
	}

	paths := splitPath(path, pathSep)
	for _, dir := range paths {
		fullPath := filepath.Join(dir, name)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath, nil
		}
	}

	return "", fmt.Errorf("%s not found in PATH", name)
}

func splitPath(path, sep string) []string {
	if path == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 0; i < len(path); i++ {
		if string(path[i]) == sep {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	if start < len(path) {
		parts = append(parts, path[start:])
	}
	return parts
}

// buildWt builds the wt binary from source
func buildWt(outputPath string) error {
	// This would use os/exec to run: go build -o outputPath .
	// For now, we'll return an error as this requires more complex setup
	return fmt.Errorf("building from source not yet implemented - please set WT_BINARY")
}

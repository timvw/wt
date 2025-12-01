package scenarios

import (
	"os/exec"
	"testing"

	"github.com/timvw/wt/e2e/harness"
)

// TestWorktreeCRUD tests the full Create, Read, Update, Delete cycle of worktrees
func TestWorktreeCRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	scenario := harness.Scenario{
		Name:        "worktree CRUD operations",
		Description: "Test create, list, and remove worktree operations",
		Setup: func(f *harness.Fixture) error {
			// Create test branches
			if err := f.CreateBranch("feature-1", "main"); err != nil {
				return err
			}
			return f.CreateBranch("feature-2", "main")
		},
		Steps: []harness.Step{
			// Create first worktree
			{Cmd: "wt", Args: []string{"checkout", "feature-1"}},
			// List should show feature-1
			{Cmd: "wt", Args: []string{"list"}},
			// Create second worktree
			{Cmd: "wt", Args: []string{"checkout", "feature-2"}},
			// List should show both
			{Cmd: "wt", Args: []string{"list"}},
		},
		Verify: []harness.Assertion{
			harness.AssertExitCode(0),
			harness.AssertStdoutContains("feature-1"),
			harness.AssertStdoutContains("feature-2"),
		},
	}

	// Run through available adapters
	adapters := []harness.ShellAdapter{
		harness.NewBashAdapter(),
	}

	// Add zsh adapter only if zsh is available
	if _, err := exec.LookPath("zsh"); err == nil {
		adapters = append(adapters, harness.NewZshAdapter())
	}

	for _, adapter := range adapters {
		t.Run(adapter.Name(), func(t *testing.T) {
			runner, err := harness.NewRunner(t, adapter)
			if err != nil {
				t.Fatalf("Failed to create runner: %v", err)
			}
			defer runner.Cleanup()

			if err := runner.Run(scenario); err != nil {
				t.Fatalf("Scenario failed: %v", err)
			}
		})
	}
}

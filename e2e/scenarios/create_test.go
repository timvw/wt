package scenarios

import (
	"testing"

	"github.com/timvw/wt/e2e/harness"
)

// TestCreateNewBranch tests that wt create works and auto-cds to the new worktree
func TestCreateNewBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	scenario := harness.Scenario{
		Name:        "create new branch with auto-cd",
		Description: "Verify wt create creates a new branch worktree and auto-cds to it",
		Setup: func(f *harness.Fixture) error {
			// No additional setup needed - main branch exists by default
			return nil
		},
		Steps: []harness.Step{
			{Cmd: "wt", Args: []string{"create", "new-feature"}},
		},
		Verify: []harness.Assertion{
			harness.AssertExitCode(0),
			harness.AssertPwdEquals("$WORKTREE_ROOT/$REPO/new-feature"),
			harness.AssertStdoutContains("TREE_ME_CD:"),
		},
	}

	// Get shell adapters from E2E_SHELLS environment variable
	adapters := getShellAdapters(t)

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

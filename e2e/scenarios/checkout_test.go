package scenarios

import (
	"os/exec"
	"testing"

	"github.com/timvw/wt/e2e/harness"
)

// TestCheckoutExistingBranch tests that wt checkout works with an existing branch
// and auto-cds to the worktree directory
func TestCheckoutExistingBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	scenario := harness.Scenario{
		Name:        "checkout existing branch with auto-cd",
		Description: "Verify wt checkout switches to existing branch worktree and auto-cds",
		Setup: func(f *harness.Fixture) error {
			// Create a test branch
			return f.CreateBranch("test-branch", "main")
		},
		Steps: []harness.Step{
			{Cmd: "wt", Args: []string{"checkout", "test-branch"}},
		},
		Verify: []harness.Assertion{
			harness.AssertExitCode(0),
			harness.AssertPwdEquals("$WORKTREE_ROOT/$REPO/test-branch"),
			harness.AssertStdoutContains("TREE_ME_CD:"),
		},
	}

	// Run through all available adapters
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

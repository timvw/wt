package harness

// ShellAdapter defines the interface for shell-specific test execution
type ShellAdapter interface {
	// Name returns the shell name (e.g., "bash", "zsh", "pwsh")
	Name() string

	// Setup initializes the shell environment with wt shellenv loaded
	Setup(wtBinary, worktreeRoot, repoDir string) error

	// Execute runs a command in the shell and captures the result
	Execute(cmd string, args []string) (*Result, error)

	// GetPwd returns the current working directory in the shell
	GetPwd() (string, error)

	// Cleanup tears down the shell adapter and cleans up resources
	Cleanup() error
}

package harness

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// BashAdapter implements ShellAdapter for bash shell
type BashAdapter struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	stdoutReader *bufio.Reader
	stderrReader *bufio.Reader
	mu           sync.Mutex
}

// NewBashAdapter creates a new bash adapter
func NewBashAdapter() *BashAdapter {
	return &BashAdapter{}
}

// Name returns the shell name
func (a *BashAdapter) Name() string {
	return "bash"
}

// Setup initializes the bash shell with wt shellenv
func (a *BashAdapter) Setup(wtBinary, worktreeRoot, repoDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Start bash in interactive mode
	a.cmd = exec.Command("bash", "-i")

	// Setup pipes
	stdin, err := a.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	a.stdin = stdin

	stdout, err := a.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	a.stdout = stdout
	a.stdoutReader = bufio.NewReader(stdout)

	stderr, err := a.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	a.stderr = stderr
	a.stderrReader = bufio.NewReader(stderr)

	// Start the shell
	if err := a.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start bash: %w", err)
	}

	// Set up environment and source shellenv
	// Disable prompt for cleaner output
	setupScript := fmt.Sprintf(`
PS1=""
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
eval "$(wt shellenv)"
echo "___SETUP_COMPLETE___"
`, worktreeRoot, dirFromBinary(wtBinary), repoDir)

	if _, err := a.stdin.Write([]byte(setupScript)); err != nil {
		return fmt.Errorf("failed to write setup script: %w", err)
	}

	// Wait for setup to complete
	if err := a.waitForMarker("___SETUP_COMPLETE___"); err != nil {
		return fmt.Errorf("failed to complete setup: %w", err)
	}

	return nil
}

// Execute runs a command in the bash shell
func (a *BashAdapter) Execute(cmd string, args []string) (*Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Build command with markers
	fullCmd := cmd
	if len(args) > 0 {
		fullCmd = fmt.Sprintf("%s %s", cmd, strings.Join(args, " "))
	}

	script := fmt.Sprintf(`
echo "___CMD_START___"
%s
__exit_code=$?
echo "___EXIT_CODE___:$__exit_code"
pwd
echo "___PWD_COMPLETE___"
echo "___CMD_END___"
`, fullCmd)

	if _, err := a.stdin.Write([]byte(script)); err != nil {
		return nil, fmt.Errorf("failed to write command: %w", err)
	}

	// Parse output
	result, err := a.parseCommandOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to parse output: %w", err)
	}

	return result, nil
}

// GetPwd returns the current working directory
func (a *BashAdapter) GetPwd() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	script := `
echo "___PWD_START___"
pwd
echo "___PWD_END___"
`

	if _, err := a.stdin.Write([]byte(script)); err != nil {
		return "", fmt.Errorf("failed to write pwd command: %w", err)
	}

	// Read until we find PWD_START
	if err := a.waitForMarker("___PWD_START___"); err != nil {
		return "", err
	}

	// Read the pwd
	pwd, err := a.stdoutReader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read pwd: %w", err)
	}
	pwd = strings.TrimSpace(pwd)

	// Wait for PWD_END
	if err := a.waitForMarker("___PWD_END___"); err != nil {
		return "", err
	}

	return pwd, nil
}

// Cleanup terminates the bash shell
func (a *BashAdapter) Cleanup() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stdin != nil {
		a.stdin.Write([]byte("exit\n"))
		a.stdin.Close()
	}

	if a.cmd != nil && a.cmd.Process != nil {
		return a.cmd.Wait()
	}

	return nil
}

// Helper functions

func (a *BashAdapter) waitForMarker(marker string) error {
	for {
		line, err := a.stdoutReader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read line: %w", err)
		}
		if strings.Contains(line, marker) {
			return nil
		}
	}
}

func (a *BashAdapter) parseCommandOutput() (*Result, error) {
	result := &Result{}
	var stdout, stderr strings.Builder
	exitCode := 0

	// Wait for CMD_START
	if err := a.waitForMarker("___CMD_START___"); err != nil {
		return nil, err
	}

	// Read until we find EXIT_CODE marker
	for {
		line, err := a.stdoutReader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read stdout: %w", err)
		}

		if strings.HasPrefix(line, "___EXIT_CODE___:") {
			// Parse exit code
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &exitCode)
			}
			break
		}

		stdout.WriteString(line)
	}

	result.Stdout = stdout.String()
	result.ExitCode = exitCode

	// Read pwd
	pwdLine, err := a.stdoutReader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("failed to read pwd: %w", err)
	}
	result.Pwd = strings.TrimSpace(pwdLine)

	// Wait for PWD_COMPLETE and CMD_END
	if err := a.waitForMarker("___PWD_COMPLETE___"); err != nil {
		return nil, err
	}
	if err := a.waitForMarker("___CMD_END___"); err != nil {
		return nil, err
	}

	result.Stderr = stderr.String()
	return result, nil
}

func dirFromBinary(binary string) string {
	// Simple path parsing - get directory containing binary
	lastSlash := strings.LastIndex(binary, "/")
	if lastSlash == -1 {
		return "."
	}
	return binary[:lastSlash]
}

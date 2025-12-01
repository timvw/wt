//go:build windows

package harness

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// PwshAdapter implements ShellAdapter for PowerShell
type PwshAdapter struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	stdoutReader *bufio.Reader
	stderrReader *bufio.Reader
	mu           sync.Mutex
}

// NewPwshAdapter creates a new PowerShell adapter
func NewPwshAdapter() *PwshAdapter {
	return &PwshAdapter{}
}

// Name returns the shell name
func (a *PwshAdapter) Name() string {
	return "pwsh"
}

// Setup initializes the PowerShell shell with wt shellenv
func (a *PwshAdapter) Setup(wtBinary, worktreeRoot, repoDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Start PowerShell with no profile and non-interactive mode
	a.cmd = exec.Command("pwsh", "-NoProfile", "-NoLogo", "-NonInteractive")

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
		return fmt.Errorf("failed to start pwsh: %w", err)
	}

	// Set up environment and source shellenv
	// PowerShell uses different syntax
	setupScript := fmt.Sprintf(`
$env:WORKTREE_ROOT = '%s'
$env:PATH = '%s;' + $env:PATH
Set-Location '%s'
Invoke-Expression (& '%s' shellenv)
Write-Output "___SETUP_COMPLETE___"
`, worktreeRoot, dirFromBinary(wtBinary), repoDir, wtBinary)

	if _, err := a.stdin.Write([]byte(setupScript)); err != nil {
		return fmt.Errorf("failed to write setup script: %w", err)
	}

	// Wait for setup to complete
	if err := a.waitForMarker("___SETUP_COMPLETE___"); err != nil {
		return fmt.Errorf("failed to complete setup: %w", err)
	}

	return nil
}

// Execute runs a command in the PowerShell shell
func (a *PwshAdapter) Execute(cmd string, args []string) (*Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Build command with markers
	fullCmd := cmd
	if len(args) > 0 {
		// Quote arguments for PowerShell
		quotedArgs := make([]string, len(args))
		for i, arg := range args {
			quotedArgs[i] = fmt.Sprintf("'%s'", arg)
		}
		fullCmd = fmt.Sprintf("%s %s", cmd, strings.Join(quotedArgs, " "))
	}

	script := fmt.Sprintf(`
Write-Output "___CMD_START___"; %s; $__exit_code = $LASTEXITCODE; Write-Output "___EXIT_CODE___:$__exit_code"; Write-Output (Get-Location).Path; Write-Output "___PWD_COMPLETE___"; Write-Output "___CMD_END___"
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
func (a *PwshAdapter) GetPwd() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	script := `
Write-Output "___PWD_START___"; Write-Output (Get-Location).Path; Write-Output "___PWD_END___"
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

// Cleanup terminates the PowerShell shell
func (a *PwshAdapter) Cleanup() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stdin != nil {
		_, _ = a.stdin.Write([]byte("exit\n"))
		a.stdin.Close()
	}

	if a.cmd != nil && a.cmd.Process != nil {
		return a.cmd.Wait()
	}

	return nil
}

// Helper functions

func (a *PwshAdapter) waitForMarker(marker string) error {
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

func (a *PwshAdapter) parseCommandOutput() (*Result, error) {
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
				_, _ = fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &exitCode)
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

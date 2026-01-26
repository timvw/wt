package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aymanbagabas/go-pty"
)

// ptyShell represents a pseudo-terminal running a shell
type ptyShell struct {
	pty       pty.Pty
	cmd       *pty.Cmd
	output    bytes.Buffer
	outputMux sync.Mutex // Protects output buffer access
	done      chan struct{}
	t         *testing.T
}

// getInitWaitTime returns appropriate wait time for shell initialization
// Longer in CI due to race detector and slower environments
func getInitWaitTime() time.Duration {
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		return 5 * time.Second
	}
	return 2 * time.Second
}

// getContextTimeout returns appropriate timeout for waiting on shell output
// Longer in CI due to race detector and slower environments
func getContextTimeout() time.Duration {
	if os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != "" {
		return 10 * time.Second
	}
	return 5 * time.Second
}

// newPtyZsh spawns zsh in a pty with the given rc content
func newPtyZsh(t *testing.T, rcContent string) (*ptyShell, error) {
	t.Helper()

	// Create a temporary directory for zsh config
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".zshrc")
	if err := os.WriteFile(rcFile, []byte(rcContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write .zshrc: %w", err)
	}

	// Create a new PTY
	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create pty: %w", err)
	}

	// Spawn zsh with custom ZDOTDIR
	cmd := p.Command("zsh", "-i")
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("ZDOTDIR=%s", tmpDir),
		"HOME="+tmpDir,
		"TERM=xterm-256color",
	)

	// Start the command
	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, fmt.Errorf("failed to start zsh with pty: %w", err)
	}

	ps := &ptyShell{
		pty:  p,
		cmd:  cmd,
		done: make(chan struct{}),
		t:    t,
	}

	// Start reading output in a goroutine
	go ps.readLoop()

	return ps, nil
}

// newPtyBash spawns bash in a pty with the given rc content
func newPtyBash(t *testing.T, rcContent string) (*ptyShell, error) {
	t.Helper()

	// Create a temporary directory for bash config
	tmpDir := t.TempDir()
	rcFile := filepath.Join(tmpDir, ".bashrc")
	if err := os.WriteFile(rcFile, []byte(rcContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write .bashrc: %w", err)
	}

	// Create a new PTY
	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create pty: %w", err)
	}

	// Spawn bash with custom --init-file (similar to --rcfile but for interactive shells)
	cmd := p.Command("bash", "--noprofile", "--init-file", rcFile)
	cmd.Env = append(os.Environ(),
		"HOME="+tmpDir,
		"TERM=xterm-256color",
	)

	// Start the command
	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, fmt.Errorf("failed to start bash with pty: %w", err)
	}

	ps := &ptyShell{
		pty:  p,
		cmd:  cmd,
		done: make(chan struct{}),
		t:    t,
	}

	// Start reading output in a goroutine
	go ps.readLoop()

	return ps, nil
}

// newPtyPowerShell spawns PowerShell in a pty with the given profile content
func newPtyPowerShell(t *testing.T, profileContent string) (*ptyShell, error) {
	t.Helper()

	// Create a temporary directory for PowerShell home
	tmpDir := t.TempDir()

	// Try pwsh first (PowerShell Core), fallback to powershell (Windows PowerShell)
	shellCmd := "pwsh"
	if _, err := exec.LookPath("pwsh"); err != nil {
		shellCmd = "powershell"
	}

	// Create a profile script file that PowerShell will execute
	profileFile := filepath.Join(tmpDir, "init.ps1")
	if err := os.WriteFile(profileFile, []byte(profileContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write init script: %w", err)
	}

	// Create a new PTY
	p, err := pty.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create pty: %w", err)
	}

	// Spawn PowerShell in interactive mode, executing the init script first
	// Use -NoProfile to avoid system profiles, -NoLogo to reduce clutter
	// Use -NoExit -Command to execute init script then stay open for interactive commands
	initCmd := fmt.Sprintf(". '%s'", profileFile)
	cmd := p.Command(shellCmd, "-NoProfile", "-NoLogo", "-NoExit", "-Command", initCmd)
	cmd.Env = append(os.Environ(),
		"HOME="+tmpDir,
		"USERPROFILE="+tmpDir,
	)

	// Start the command
	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, fmt.Errorf("failed to start %s with pty: %w", shellCmd, err)
	}

	ps := &ptyShell{
		pty:  p,
		cmd:  cmd,
		done: make(chan struct{}),
		t:    t,
	}

	// Start reading output in a goroutine
	go ps.readLoop()

	return ps, nil
}

// readLoop continuously reads from the pty and appends to the output buffer
func (ps *ptyShell) readLoop() {
	defer close(ps.done)
	buf := make([]byte, 4096)
	for {
		n, err := ps.pty.Read(buf)
		if n > 0 {
			ps.outputMux.Lock()
			ps.output.Write(buf[:n])
			ps.outputMux.Unlock()
		}
		if err != nil {
			if err != io.EOF {
				ps.t.Logf("pty read error: %v", err)
			}
			return
		}
	}
}

// waitForText waits for specific text to appear in the output
func (ps *ptyShell) waitForText(ctx context.Context, text string) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	ps.outputMux.Lock()
	initialLen := ps.output.Len()
	ps.outputMux.Unlock()

	lastLen := initialLen
	stableCount := 0

	for {
		select {
		case <-ctx.Done():
			ps.outputMux.Lock()
			outputStr := ps.output.String()
			ps.outputMux.Unlock()
			return fmt.Errorf("timeout waiting for text '%s': %w\nGot output:\n%s",
				text, ctx.Err(), outputStr)
		case <-ticker.C:
			ps.outputMux.Lock()
			output := ps.output.String()
			currentLen := ps.output.Len()
			ps.outputMux.Unlock()

			// Check if we found the text
			if strings.Contains(output, text) {
				return nil
			}

			// Check if output has stabilized (no new data)
			if currentLen == lastLen {
				stableCount++
				// If output hasn't changed for 1 second (10 ticks), we're likely stuck
				if stableCount >= 10 {
					return fmt.Errorf("output stabilized without finding text '%s'\nGot output:\n%s",
						text, output)
				}
			} else {
				stableCount = 0
			}
			lastLen = currentLen
		}
	}
}

// send writes a string to the pty (simulating user input)
func (ps *ptyShell) send(s string) error {
	_, err := ps.pty.Write([]byte(s))
	return err
}

// close terminates the shell and cleans up resources
func (ps *ptyShell) close() {
	ps.send("exit\r\n")

	// Wait for process with timeout to avoid hanging forever
	done := make(chan struct{})
	go func() {
		ps.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Process exited normally
	case <-time.After(5 * time.Second):
		// Timeout - force kill
		ps.t.Logf("Shell process didn't exit within timeout, force killing")
		ps.cmd.Process.Kill()
		<-done
	}

	ps.pty.Close()
	<-ps.done
}

// getOutput returns the current accumulated output
func (ps *ptyShell) getOutput() string {
	ps.outputMux.Lock()
	defer ps.outputMux.Unlock()
	return ps.output.String()
}

// resetOutput clears the output buffer (thread-safe)
func (ps *ptyShell) resetOutput() {
	ps.outputMux.Lock()
	defer ps.outputMux.Unlock()
	ps.output.Reset()
}

// TestInteractiveCheckoutWithoutArgs demonstrates the hang when running 'wt co'
// without providing a branch name. This test should FAIL until the bug is fixed.
func TestInteractiveCheckoutWithoutArgs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	// Check if zsh is available
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available, skipping zsh interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	// Setup test repo
	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create test branches
	runGitCommand(t, repoDir, "checkout", "-b", "feature-1")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit 1")
	runGitCommand(t, repoDir, "checkout", "main")
	runGitCommand(t, repoDir, "checkout", "-b", "feature-2")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit 2")
	runGitCommand(t, repoDir, "checkout", "main")

	// Create zsh rc that sources wt shellenv and cd's to repo
	// Use explicit path to the built binary to avoid using system wt
	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
type wt | head -n 1
echo "Built wt binary: %s"
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary, wtBinary)

	// Launch zsh with our config
	ps, err := newPtyZsh(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty zsh: %v", err)
	}
	defer ps.close()

	// Wait a bit for shell to initialize
	time.Sleep(getInitWaitTime())
	t.Logf("Initial output from zsh:\n%s", ps.getOutput())

	// Wait for the shellenv loaded marker
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	t.Log("Shellenv loaded, sending 'wt co' command...")

	// Clear the buffer to focus on the command output
	ps.resetOutput()

	// Send the interactive command
	if err := ps.send("wt co\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Try to wait for the branch selection prompt to appear
	// This demonstrates the hang - we expect to see the prompt but it never appears
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	err = ps.waitForText(ctx2, "Select branch to checkout")
	if err != nil {
		// This is the EXPECTED behavior with the bug - the prompt never appears
		t.Logf("BUG CONFIRMED: Interactive prompt did not appear within timeout")
		t.Logf("Output captured:\n%s", ps.getOutput())
		t.Fatalf("Interactive checkout hung: %v", err)
	}

	// If we reach here, the bug is fixed!
	t.Log("SUCCESS: Interactive prompt appeared!")
	t.Log("The bug appears to be fixed.")

	// Cancel the prompt and exit cleanly
	ps.send("\x03") // Ctrl-C to cancel the prompt
	time.Sleep(500 * time.Millisecond)
}

// TestNonInteractiveCheckoutWithArgs demonstrates that checkout works when
// providing an explicit branch name. This test should PASS.
func TestNonInteractiveCheckoutWithArgs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	// Check if zsh is available
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available, skipping zsh interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	// Setup test repo
	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create a test branch
	runGitCommand(t, repoDir, "checkout", "-b", "feature-explicit")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit")
	runGitCommand(t, repoDir, "checkout", "main")

	// Create zsh rc that sources wt shellenv and cd's to repo
	// Use explicit path to the built binary to avoid using system wt
	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
type wt | head -n 1
echo "Built wt binary: %s"
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary, wtBinary)

	// Launch zsh with our config
	ps, err := newPtyZsh(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty zsh: %v", err)
	}
	defer ps.close()

	// Wait a bit for shell to initialize
	time.Sleep(getInitWaitTime())
	t.Logf("Initial output from zsh:\n%s", ps.getOutput())

	// Wait for the shellenv loaded marker
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	t.Log("Shellenv loaded, sending 'wt co feature-explicit' command...")

	// Clear the buffer to focus on the command output
	ps.resetOutput()

	// Send the non-interactive command with explicit branch name
	if err := ps.send("wt co feature-explicit\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Wait for the success message
	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()

	err = ps.waitForText(ctx2, "Worktree created at:")
	if err != nil {
		t.Fatalf("Non-interactive checkout failed: %v\nOutput:\n%s", err, ps.getOutput())
	}

	// Also verify the TREE_ME_CD marker is present
	output := ps.getOutput()
	expectedPath := filepath.Join(worktreeRoot, "test-repo", "feature-explicit")
	if !strings.Contains(output, "TREE_ME_CD:"+expectedPath) {
		t.Errorf("TREE_ME_CD marker not found in output.\nExpected path: %s\nOutput:\n%s",
			expectedPath, output)
	}

	t.Log("SUCCESS: Non-interactive checkout with explicit branch name works correctly")
}

// TestInteractiveCheckoutWithoutArgsBash demonstrates the v0.1.12 hang bug when running 'wt co'
// without providing a branch name in bash. This test should PASS after the fix.
func TestInteractiveCheckoutWithoutArgsBash(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	// Check if bash is available
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping bash interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	// Setup test repo
	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create test branches
	runGitCommand(t, repoDir, "checkout", "-b", "feature-1")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit 1")
	runGitCommand(t, repoDir, "checkout", "main")
	runGitCommand(t, repoDir, "checkout", "-b", "feature-2")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit 2")
	runGitCommand(t, repoDir, "checkout", "main")

	// Create bash rc that sources wt shellenv and cd's to repo
	// Use explicit path to the built binary to avoid using system wt
	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
type wt | head -n 1
echo "Built wt binary: %s"
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary, wtBinary)

	// Launch bash with our config
	ps, err := newPtyBash(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty bash: %v", err)
	}
	defer ps.close()

	// Wait a bit for shell to initialize
	time.Sleep(getInitWaitTime())
	t.Logf("Initial output from bash:\n%s", ps.getOutput())

	// Wait for the shellenv loaded marker
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	t.Log("Shellenv loaded, sending 'wt co' command...")

	// Clear the buffer to focus on the command output
	ps.resetOutput()

	// Send the interactive command
	if err := ps.send("wt co\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Try to wait for the branch selection prompt to appear
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	err = ps.waitForText(ctx2, "Select branch to checkout")
	if err != nil {
		// This is the EXPECTED behavior with the bug - the prompt never appears
		t.Logf("BUG CONFIRMED: Interactive prompt did not appear within timeout")
		t.Logf("Output captured:\n%s", ps.getOutput())
		t.Fatalf("Interactive checkout hung: %v", err)
	}

	// If we reach here, the bug is fixed!
	t.Log("SUCCESS: Interactive prompt appeared!")
	t.Log("The bug appears to be fixed.")

	// Cancel the prompt and exit cleanly
	ps.send("\x03") // Ctrl-C to cancel the prompt
	time.Sleep(500 * time.Millisecond)
}

// TestNonInteractiveCheckoutWithArgsBash demonstrates that checkout works when
// providing an explicit branch name in bash. This test should PASS.
func TestNonInteractiveCheckoutWithArgsBash(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	// Check if bash is available
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping bash interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	// Setup test repo
	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create a test branch
	runGitCommand(t, repoDir, "checkout", "-b", "feature-explicit")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit")
	runGitCommand(t, repoDir, "checkout", "main")

	// Create bash rc that sources wt shellenv and cd's to repo
	// Use explicit path to the built binary to avoid using system wt
	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
type wt | head -n 1
echo "Built wt binary: %s"
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary, wtBinary)

	// Launch bash with our config
	ps, err := newPtyBash(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty bash: %v", err)
	}
	defer ps.close()

	// Wait a bit for shell to initialize
	time.Sleep(getInitWaitTime())
	t.Logf("Initial output from bash:\n%s", ps.getOutput())

	// Wait for the shellenv loaded marker
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	t.Log("Shellenv loaded, sending 'wt co feature-explicit' command...")

	// Clear the buffer to focus on the command output
	ps.resetOutput()

	// Send the non-interactive command with explicit branch name
	if err := ps.send("wt co feature-explicit\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Wait for the success message
	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()

	err = ps.waitForText(ctx2, "Worktree created at:")
	if err != nil {
		t.Fatalf("Non-interactive checkout failed: %v\nOutput:\n%s", err, ps.getOutput())
	}

	// Also verify the TREE_ME_CD marker is present
	output := ps.getOutput()
	expectedPath := filepath.Join(worktreeRoot, "test-repo", "feature-explicit")
	if !strings.Contains(output, "TREE_ME_CD:"+expectedPath) {
		t.Errorf("TREE_ME_CD marker not found in output.\nExpected path: %s\nOutput:\n%s",
			expectedPath, output)
	}

	t.Log("SUCCESS: Non-interactive checkout with explicit branch name works correctly")
}

// TestInteractiveCheckoutWithoutArgsPowerShell demonstrates the interactive 'wt co'
// prompt in PowerShell. Tests that interactive prompts work correctly.
func TestInteractiveCheckoutWithoutArgsPowerShell(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	// PowerShell PTY tests only work on Windows due to upstream PowerShell bug #14932
	// https://github.com/PowerShell/PowerShell/issues/14932
	if runtime.GOOS != "windows" {
		t.Skip("Skipping PowerShell PTY test on non-Windows (upstream bug #14932)")
	}

	// Check if pwsh or powershell is available
	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell not available, skipping PowerShell interactive test")
		}
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	// Setup test repo
	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create test branches
	runGitCommand(t, repoDir, "checkout", "-b", "feature-1")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit 1")
	runGitCommand(t, repoDir, "checkout", "main")
	runGitCommand(t, repoDir, "checkout", "-b", "feature-2")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit 2")
	runGitCommand(t, repoDir, "checkout", "main")

	// Create PowerShell profile that sources wt shellenv and cd's to repo
	// Use Windows path format for binary
	wtBinaryWin := filepath.ToSlash(wtBinary)
	repoDirWin := filepath.ToSlash(repoDir)
	worktreeRootWin := filepath.ToSlash(worktreeRoot)
	binDir := filepath.ToSlash(filepath.Dir(wtBinary))

	profileContent := fmt.Sprintf(`
$env:WORKTREE_ROOT = '%s'
$env:PATH = '%s;' + $env:PATH
Set-Location '%s'
& '%s' shellenv | Out-String | Invoke-Expression
Write-Output "=== WT SHELLENV LOADED ==="
Get-Command wt | Select-Object -ExpandProperty CommandType
Write-Output "Built wt binary: %s"
`, worktreeRootWin, binDir, repoDirWin, wtBinaryWin, wtBinaryWin)

	// Launch PowerShell with our profile
	ps, err := newPtyPowerShell(t, profileContent)
	if err != nil {
		t.Fatalf("Failed to create pty PowerShell: %v", err)
	}
	defer ps.close()

	// Wait a bit for shell to initialize
	time.Sleep(getInitWaitTime())
	t.Logf("Initial output from PowerShell:\n%s", ps.getOutput())

	// Wait for the shellenv loaded marker
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	t.Log("Shellenv loaded, sending 'wt co' command...")

	// Clear the buffer to focus on the command output
	ps.resetOutput()

	// Send the interactive command
	if err := ps.send("wt co\r\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Try to wait for the branch selection prompt to appear
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	err = ps.waitForText(ctx2, "Select branch to checkout")
	if err != nil {
		// This is the EXPECTED behavior with the bug - the prompt never appears
		t.Logf("BUG CONFIRMED: Interactive prompt did not appear within timeout")
		t.Logf("Output captured:\n%s", ps.getOutput())
		t.Fatalf("Interactive checkout hung: %v", err)
	}

	// If we reach here, the bug is fixed!
	t.Log("SUCCESS: Interactive prompt appeared!")
	t.Log("The bug appears to be fixed.")

	// Cancel the prompt and exit cleanly
	ps.send("\x03") // Ctrl-C to cancel the prompt
	time.Sleep(500 * time.Millisecond)
}

// TestNonInteractiveCheckoutWithArgsPowerShell demonstrates that checkout works when
// providing an explicit branch name in PowerShell. This test should PASS.
func TestNonInteractiveCheckoutWithArgsPowerShell(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	// PowerShell PTY tests only work on Windows due to upstream PowerShell bug #14932
	// https://github.com/PowerShell/PowerShell/issues/14932
	if runtime.GOOS != "windows" {
		t.Skip("Skipping PowerShell PTY test on non-Windows (upstream bug #14932)")
	}

	// Check if pwsh or powershell is available
	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell not available, skipping PowerShell interactive test")
		}
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	// Setup test repo
	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// Create a test branch
	runGitCommand(t, repoDir, "checkout", "-b", "feature-explicit")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit")
	runGitCommand(t, repoDir, "checkout", "main")

	// Create PowerShell profile
	wtBinaryWin := filepath.ToSlash(wtBinary)
	repoDirWin := filepath.ToSlash(repoDir)
	worktreeRootWin := filepath.ToSlash(worktreeRoot)
	binDir := filepath.ToSlash(filepath.Dir(wtBinary))

	profileContent := fmt.Sprintf(`
$env:WORKTREE_ROOT = '%s'
$env:PATH = '%s;' + $env:PATH
Set-Location '%s'
& '%s' shellenv | Out-String | Invoke-Expression
Write-Output "=== WT SHELLENV LOADED ==="
`, worktreeRootWin, binDir, repoDirWin, wtBinaryWin)

	// Launch PowerShell with our profile
	ps, err := newPtyPowerShell(t, profileContent)
	if err != nil {
		t.Fatalf("Failed to create pty PowerShell: %v", err)
	}
	defer ps.close()

	// Wait for shell to initialize
	time.Sleep(getInitWaitTime())
	t.Logf("Initial output from PowerShell:\n%s", ps.getOutput())

	// Wait for the shellenv loaded marker
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	t.Log("Shellenv loaded, sending 'wt co feature-explicit' command...")

	// Clear the buffer to focus on the command output
	ps.resetOutput()

	// Send the non-interactive command with explicit branch name
	if err := ps.send("wt co feature-explicit\r\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Wait for the success message
	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()

	err = ps.waitForText(ctx2, "Worktree created at:")
	if err != nil {
		t.Fatalf("Non-interactive checkout failed: %v\nOutput:\n%s", err, ps.getOutput())
	}

	// Also verify the TREE_ME_CD marker is present
	output := ps.getOutput()
	expectedPath := filepath.Join(worktreeRoot, "test-repo", "feature-explicit")
	if !strings.Contains(output, "TREE_ME_CD:"+expectedPath) {
		t.Errorf("TREE_ME_CD marker not found in output.\nExpected path: %s\nOutput:\n%s",
			expectedPath, output)
	}

	t.Log("SUCCESS: Non-interactive checkout with explicit branch name works correctly")
}

// Helper functions for test setup

func setupTestRepo(t *testing.T, repoDir string) {
	t.Helper()

	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("Failed to create repo dir: %v", err)
	}

	// Initialize git repo
	runGitCommand(t, repoDir, "init")
	runGitCommand(t, repoDir, "config", "user.email", "test@example.com")
	runGitCommand(t, repoDir, "config", "user.name", "Test User")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "initial commit")
	runGitCommand(t, repoDir, "branch", "-M", "main")
}

func buildWtBinary(t *testing.T, tmpDir string) string {
	t.Helper()

	binaryName := "wt"
	// On Windows, executables need .exe extension
	if filepath.Separator == '\\' {
		binaryName = "wt.exe"
	}

	binaryPath := filepath.Join(tmpDir, binaryName)
	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Failed to build wt binary: %v\nOutput: %s", err, output)
	}

	return binaryPath
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Git command failed: git %v\nError: %v\nOutput: %s",
			args, err, output)
	}
}

package cmd

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

var (
	builtWtBinaryOnce sync.Once
	builtWtBinaryPath string
	builtWtBinaryErr  error
)

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
			ps.outputMux.Unlock()

			// Check if we found the text
			if strings.Contains(output, text) {
				return nil
			}
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

// TestInteractiveCheckoutWithoutArgs verifies interactive checkout prompt works in zsh
// when running 'wt co' without a branch argument.
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

	// Wait for the branch selection prompt to appear
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	err = ps.waitForText(ctx2, "Select branch to checkout")
	if err != nil {
		t.Fatalf("Interactive checkout prompt did not appear: %v\nOutput:\n%s", err, ps.getOutput())
	}

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

	// Also verify the navigation marker is present
	output := ps.getOutput()
	expectedPath := filepath.Join(worktreeRoot, "test-repo", "feature-explicit")
	if !strings.Contains(output, "wt navigating to: "+expectedPath) {
		t.Errorf("navigation marker not found in output.\nExpected path: %s\nOutput:\n%s",
			expectedPath, output)
	}

	t.Log("SUCCESS: Non-interactive checkout with explicit branch name works correctly")
}

// TestZshTabCompletionWithShellenvLast verifies positive zsh tab completion behavior
// when shellenv is sourced after other completion initialization.
func TestZshTabCompletionWithShellenvLast(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available, skipping zsh interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	branch := "AIG-zsh-completion-branch"
	existingPath := filepath.Join(tmpDir, "existing-zsh-completion-worktree")
	runGitCommand(t, repoDir, "worktree", "add", "-b", branch, existingPath, "main")

	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
autoload -Uz compinit
compinit
eval "$(printf 'autoload -Uz compinit\ncompinit\n')"
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	ps, err := newPtyZsh(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty zsh: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	if err := ps.send("echo _COMPS_WT=${_comps[wt]-unset}\n"); err != nil {
		t.Fatalf("Failed to send _comps check command: %v", err)
	}

	ctxComps, cancelComps := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancelComps()
	if err := ps.waitForText(ctxComps, "_COMPS_WT=_wt_complete_zsh"); err != nil {
		t.Fatalf("wt completion mapping missing after shell init: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	if err := ps.send("wt co AIG\t\n"); err != nil {
		t.Fatalf("Failed to send completion command: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, "Worktree already exists:"); err != nil {
		t.Fatalf("zsh tab completion checkout failed: %v\nOutput:\n%s", err, ps.getOutput())
	}

	if !strings.Contains(ps.getOutput(), branch) {
		t.Fatalf("expected completed branch name %q in output, got:\n%s", branch, ps.getOutput())
	}
}

// TestInteractiveCheckoutWithoutArgsBash verifies interactive checkout prompt works in bash
// when running 'wt co' without a branch argument.
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

	// Wait for the branch selection prompt to appear
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	err = ps.waitForText(ctx2, "Select branch to checkout")
	if err != nil {
		t.Fatalf("Interactive checkout prompt did not appear: %v\nOutput:\n%s", err, ps.getOutput())
	}

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

	// Also verify the navigation marker is present
	output := ps.getOutput()
	expectedPath := filepath.Join(worktreeRoot, "test-repo", "feature-explicit")
	if !strings.Contains(output, "wt navigating to: "+expectedPath) {
		t.Errorf("navigation marker not found in output.\nExpected path: %s\nOutput:\n%s",
			expectedPath, output)
	}

	t.Log("SUCCESS: Non-interactive checkout with explicit branch name works correctly")
}

// TestBashTabCompletionForCheckoutBranch verifies positive bash tab completion
// for checkout branch names from existing worktrees.
func TestBashTabCompletionForCheckoutBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping bash interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	branch := "AIG-bash-completion-branch"
	existingPath := filepath.Join(tmpDir, "existing-bash-completion-worktree")
	runGitCommand(t, repoDir, "worktree", "add", "-b", branch, existingPath, "main")

	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	ps, err := newPtyBash(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty bash: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	if err := ps.send("wt co AIG\t\n"); err != nil {
		t.Fatalf("Failed to send completion command: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, "Worktree already exists:"); err != nil {
		t.Fatalf("bash tab completion checkout failed: %v\nOutput:\n%s", err, ps.getOutput())
	}

	if !strings.Contains(ps.getOutput(), branch) {
		t.Fatalf("expected completed branch name %q in output, got:\n%s", branch, ps.getOutput())
	}
}

// TestZshTabCompletionForCommands verifies zsh command completion expands
// subcommands (e.g., "ve" -> "version") and executes the completed command.
func TestZshTabCompletionForCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available, skipping zsh interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
autoload -Uz compinit
compinit
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	ps, err := newPtyZsh(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty zsh: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	if err := ps.send("wt ve\t\n"); err != nil {
		t.Fatalf("Failed to send command completion input: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, "wt version "); err != nil {
		t.Fatalf("zsh command completion did not execute 'wt version': %v\nOutput:\n%s", err, ps.getOutput())
	}
	if strings.Contains(ps.getOutput(), "unknown command") {
		t.Fatalf("zsh command completion executed an unexpected command:\n%s", ps.getOutput())
	}
}

// TestBashTabCompletionForCommands verifies bash command completion expands
// subcommands (e.g., "ve" -> "version") and executes the completed command.
func TestBashTabCompletionForCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping bash interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	ps, err := newPtyBash(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty bash: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	if err := ps.send("wt ve\t\n"); err != nil {
		t.Fatalf("Failed to send command completion input: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, "wt version "); err != nil {
		t.Fatalf("bash command completion did not execute 'wt version': %v\nOutput:\n%s", err, ps.getOutput())
	}
	if strings.Contains(ps.getOutput(), "unknown command") {
		t.Fatalf("bash command completion executed an unexpected command:\n%s", ps.getOutput())
	}
}

// TestZshTabCompletionForConfigSubcommands verifies zsh completes config subcommands
// (e.g., "wt config pa<Tab>" -> "wt config path").
func TestZshTabCompletionForConfigSubcommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available, skipping zsh interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
autoload -Uz compinit
compinit
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	ps, err := newPtyZsh(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty zsh: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	if err := ps.send("wt config pa\t\n"); err != nil {
		t.Fatalf("Failed to send config subcommand completion input: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, "config.toml"); err != nil {
		t.Fatalf("zsh config subcommand completion failed: %v\nOutput:\n%s", err, ps.getOutput())
	}
}

// TestBashTabCompletionForConfigSubcommands verifies bash completes config subcommands
// (e.g., "wt config pa<Tab>" -> "wt config path").
func TestBashTabCompletionForConfigSubcommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available, skipping bash interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	ps, err := newPtyBash(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty bash: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	if err := ps.send("wt config pa\t\n"); err != nil {
		t.Fatalf("Failed to send config subcommand completion input: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, "config.toml"); err != nil {
		t.Fatalf("bash config subcommand completion failed: %v\nOutput:\n%s", err, ps.getOutput())
	}
}

// TestInteractiveCheckoutWithoutArgsPowerShell verifies interactive checkout prompt
// in PowerShell when running 'wt co' without a branch argument.
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

	// Wait for the branch selection prompt to appear
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()

	err = ps.waitForText(ctx2, "Select branch to checkout")
	if err != nil {
		t.Fatalf("Interactive checkout prompt did not appear: %v\nOutput:\n%s", err, ps.getOutput())
	}

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

	// Also verify the navigation marker is present
	output := ps.getOutput()
	expectedPath := filepath.Join(worktreeRoot, "test-repo", "feature-explicit")
	if !strings.Contains(output, "wt navigating to: "+expectedPath) {
		t.Errorf("navigation marker not found in output.\nExpected path: %s\nOutput:\n%s",
			expectedPath, output)
	}

	t.Log("SUCCESS: Non-interactive checkout with explicit branch name works correctly")
}

// TestPowerShellCompletionForCheckoutBranch verifies positive PowerShell completion
// by querying the completion API after shellenv registration.
func TestPowerShellCompletionForCheckoutBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if runtime.GOOS != "windows" {
		t.Skip("Skipping PowerShell PTY test on non-Windows (upstream bug #14932)")
	}

	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell not available, skipping PowerShell interactive test")
		}
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	branch := "AIG-pwsh-completion-branch"
	existingPath := filepath.Join(tmpDir, "existing-pwsh-completion-worktree")
	runGitCommand(t, repoDir, "worktree", "add", "-b", branch, existingPath, "main")

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

	ps, err := newPtyPowerShell(t, profileContent)
	if err != nil {
		t.Fatalf("Failed to create pty PowerShell: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	completionCmd := "$line = 'wt co AIG'; $cursor = $line.Length; [System.Management.Automation.CommandCompletion]::CompleteInput($line, $cursor, $null).CompletionMatches | ForEach-Object { $_.CompletionText }\r\n"
	if err := ps.send(completionCmd); err != nil {
		t.Fatalf("Failed to send completion API command: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, branch); err != nil {
		t.Fatalf("PowerShell completion did not return expected branch %q: %v\nOutput:\n%s", branch, err, ps.getOutput())
	}
}

// TestPowerShellCompletionForCommands verifies command completion includes
// the expected 'version' subcommand.
func TestPowerShellCompletionForCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if runtime.GOOS != "windows" {
		t.Skip("Skipping PowerShell PTY test on non-Windows (upstream bug #14932)")
	}

	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell not available, skipping PowerShell interactive test")
		}
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

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

	ps, err := newPtyPowerShell(t, profileContent)
	if err != nil {
		t.Fatalf("Failed to create pty PowerShell: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	completionCmd := "$line = 'wt ve'; $cursor = $line.Length; [System.Management.Automation.CommandCompletion]::CompleteInput($line, $cursor, $null).CompletionMatches | ForEach-Object { $_.CompletionText }\r\n"
	if err := ps.send(completionCmd); err != nil {
		t.Fatalf("Failed to send completion API command: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, "version"); err != nil {
		t.Fatalf("PowerShell command completion did not return 'version': %v\nOutput:\n%s", err, ps.getOutput())
	}
}

// TestPowerShellCompletionForConfigSubcommands verifies completion includes
// expected config subcommands for the second argument.
func TestPowerShellCompletionForConfigSubcommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if runtime.GOOS != "windows" {
		t.Skip("Skipping PowerShell PTY test on non-Windows (upstream bug #14932)")
	}

	if _, err := exec.LookPath("pwsh"); err != nil {
		if _, err := exec.LookPath("powershell"); err != nil {
			t.Skip("PowerShell not available, skipping PowerShell interactive test")
		}
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

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

	ps, err := newPtyPowerShell(t, profileContent)
	if err != nil {
		t.Fatalf("Failed to create pty PowerShell: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	completionCmd := "$line = 'wt config pa'; $cursor = $line.Length; [System.Management.Automation.CommandCompletion]::CompleteInput($line, $cursor, $null).CompletionMatches | ForEach-Object { $_.CompletionText }\r\n"
	if err := ps.send(completionCmd); err != nil {
		t.Fatalf("Failed to send completion API command: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel2()
	if err := ps.waitForText(ctx2, "path"); err != nil {
		t.Fatalf("PowerShell config subcommand completion did not return 'path': %v\nOutput:\n%s", err, ps.getOutput())
	}
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
	_ = tmpDir // Kept for backward compatibility with existing call sites.

	builtWtBinaryOnce.Do(func() {
		buildDir, err := os.MkdirTemp("", "wt-e2e-binary-")
		if err != nil {
			builtWtBinaryErr = fmt.Errorf("failed to create temp dir for wt binary: %w", err)
			return
		}

		binaryName := "wt"
		if filepath.Separator == '\\' {
			binaryName = "wt.exe"
		}

		builtWtBinaryPath = filepath.Join(buildDir, binaryName)
		cmd := exec.Command("go", "build", "-o", builtWtBinaryPath, "github.com/timvw/wt")
		if output, err := cmd.CombinedOutput(); err != nil {
			builtWtBinaryErr = fmt.Errorf("failed to build wt binary: %v\nOutput: %s", err, output)
			return
		}
	})

	if builtWtBinaryErr != nil {
		t.Fatal(builtWtBinaryErr)
	}

	return builtWtBinaryPath
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

// TestInteractiveCdWithoutArgs verifies that `wt cd` without arguments offers
// only worktrees that already exist, unlike `wt co` which lists all branches.
func TestInteractiveCdWithoutArgs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping interactive e2e test in short mode")
	}

	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available, skipping zsh interactive test")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)

	// One branch with a worktree, one without: only the former may be offered.
	runGitCommand(t, repoDir, "checkout", "-b", "with-worktree")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit 1")
	runGitCommand(t, repoDir, "checkout", "main")
	runGitCommand(t, repoDir, "checkout", "-b", "without-worktree")
	runGitCommand(t, repoDir, "commit", "--allow-empty", "-m", "test commit 2")
	runGitCommand(t, repoDir, "checkout", "main")
	runGitCommand(t, repoDir, "worktree", "add",
		filepath.Join(tmpDir, "existing-worktree"), "with-worktree")

	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s:$PATH
cd %s
source <(%s shellenv)
echo "=== WT SHELLENV LOADED ==="
`, worktreeRoot, filepath.Dir(wtBinary), repoDir, wtBinary)

	ps, err := newPtyZsh(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty zsh: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())
	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()
	if err := ps.send("wt cd\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	if err := ps.waitForText(ctx2, "Select worktree"); err != nil {
		t.Fatalf("Interactive cd prompt did not appear: %v\nOutput:\n%s", err, ps.getOutput())
	}

	if err := ps.waitForText(ctx2, "with-worktree"); err != nil {
		t.Fatalf("Expected existing worktree in selection: %v\nOutput:\n%s", err, ps.getOutput())
	}
	if strings.Contains(ps.getOutput(), "without-worktree") {
		t.Fatalf("Branch without a worktree must not be offered:\n%s", ps.getOutput())
	}

	ps.send("\x03") // Ctrl-C to cancel the prompt
	time.Sleep(500 * time.Millisecond)
}

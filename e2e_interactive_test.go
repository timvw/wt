package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// ptyShell represents a pseudo-terminal running a shell
type ptyShell struct {
	pty    *os.File
	cmd    *exec.Cmd
	output bytes.Buffer
	done   chan struct{}
	t      *testing.T
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

	// Spawn zsh with custom ZDOTDIR
	cmd := exec.Command("zsh", "-i")
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("ZDOTDIR=%s", tmpDir),
		"HOME="+tmpDir,
		"TERM=xterm-256color",
	)

	// Start the command with a PTY
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start zsh with pty: %w", err)
	}

	ps := &ptyShell{
		pty:  ptmx,
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
			ps.output.Write(buf[:n])
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

	initialLen := ps.output.Len()
	lastLen := initialLen
	stableCount := 0

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for text '%s': %w\nGot output:\n%s",
				text, ctx.Err(), ps.output.String())
		case <-ticker.C:
			output := ps.output.String()
			currentLen := ps.output.Len()

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
	ps.send("exit\n")
	ps.cmd.Wait()
	ps.pty.Close()
	<-ps.done
}

// getOutput returns the current accumulated output
func (ps *ptyShell) getOutput() string {
	return ps.output.String()
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
	time.Sleep(2 * time.Second)
	t.Logf("Initial output from zsh:\n%s", ps.getOutput())

	// Wait for the shellenv loaded marker
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	t.Log("Shellenv loaded, sending 'wt co' command...")

	// Clear the buffer to focus on the command output
	ps.output.Reset()

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
	time.Sleep(2 * time.Second)
	t.Logf("Initial output from zsh:\n%s", ps.getOutput())

	// Wait for the shellenv loaded marker
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ps.waitForText(ctx, "=== WT SHELLENV LOADED ==="); err != nil {
		t.Fatalf("Failed to load shellenv: %v\nOutput:\n%s", err, ps.getOutput())
	}

	t.Log("Shellenv loaded, sending 'wt co feature-explicit' command...")

	// Clear the buffer to focus on the command output
	ps.output.Reset()

	// Send the non-interactive command with explicit branch name
	if err := ps.send("wt co feature-explicit\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// Wait for the success message
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
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

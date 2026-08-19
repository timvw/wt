package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Regression coverage for issue #124.
//
// The shell integration's wt() wrapper has to capture stdout to find the
// "wt navigating to:" marker. Where script(1) exists it does that through a
// PTY, so interactive prompts still reach the terminal. Git Bash ships no
// script(1), so wt() falls back to a plain `command wt "$@" > "$log_file"`
// redirect (see writeBashZshShellenv). Anything a prompt writes to stdout then
// lands in the temp file instead of on screen: the menu is invisible, though it
// still accepts keystrokes, and the trailing `cat` replays it only after the
// prompt is over.
//
// The fix is promptOutput() — prompts render to stderr, which no integration
// redirects. These tests pin that by driving a real PTY down the scriptless
// branch and asserting the menu is actually on screen while the prompt is open.

// scriptlessToolchain is what the fallback branch of wt() and wt itself need in
// order to run once script(1) has been hidden from PATH.
var scriptlessToolchain = []string{"git", "grep", "sed", "mktemp", "cat", "tail", "rm", "uname"}

// scriptlessPATH returns a PATH value on which script(1) cannot be found, so a
// shell sourcing the integration takes the Git Bash fallback branch.
//
// On Windows this needs no help: Git Bash genuinely ships no script(1), which
// is the whole reason #124 exists there. Elsewhere script(1) normally sits in
// the same directory as grep and sed, so the directory cannot simply be dropped
// and a whitelist of symlinks is built instead.
func scriptlessPATH(t *testing.T, extraDirs ...string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		return os.Getenv("PATH")
	}

	shim := t.TempDir()
	for _, tool := range scriptlessToolchain {
		src, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not available, cannot build a scriptless PATH", tool)
		}
		if err := os.Symlink(src, filepath.Join(shim, tool)); err != nil {
			t.Fatalf("failed to link %s into shim PATH: %v", tool, err)
		}
	}

	path := shim
	for _, d := range extraDirs {
		path += string(os.PathListSeparator) + d
	}
	return path
}

// TestInteractivePromptVisibleWithoutScript reproduces #124: with script(1)
// unavailable the wrapper redirects stdout to a file, so a prompt rendering to
// stdout never reaches the terminal. It fails before promptOutput() was
// introduced and passes after.
func TestInteractivePromptVisibleWithoutScript(t *testing.T) {
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

	runGitCommand(t, repoDir, "branch", "feature-1")
	runGitCommand(t, repoDir, "branch", "feature-2")

	// The wt binary lives outside the shim, so its directory is appended.
	path := scriptlessPATH(t, filepath.Dir(wtBinary))

	// SCRIPTLESS_CONFIRMED guards the test itself: if script(1) were reachable
	// the wrapper would take the PTY branch, the prompt would be visible for
	// reasons unrelated to the fix, and this test would pass while guarding
	// nothing.
	rcContent := fmt.Sprintf(`
export WORKTREE_ROOT=%s
export PATH=%s
cd %s
eval "$(%s shellenv bash)"
if command -v script >/dev/null 2>&1; then
    echo "=== SCRIPT STILL ON PATH ==="
else
    echo "=== SCRIPTLESS CONFIRMED ==="
fi
`, worktreeRoot, path, repoDir, wtBinary)

	ps, err := newPtyBash(t, rcContent)
	if err != nil {
		t.Fatalf("Failed to create pty bash: %v", err)
	}
	defer ps.close()

	time.Sleep(getInitWaitTime())

	ctx, cancel := context.WithTimeout(context.Background(), getContextTimeout())
	defer cancel()
	if err := ps.waitForText(ctx, "=== SCRIPTLESS CONFIRMED ==="); err != nil {
		t.Fatalf("shim PATH did not hide script(1), so the fallback branch is not "+
			"being exercised: %v\nOutput:\n%s", err, ps.getOutput())
	}

	ps.resetOutput()

	if err := ps.send("wt co\n"); err != nil {
		t.Fatalf("Failed to send command: %v", err)
	}

	// The assertion: the menu must reach the *terminal* while the prompt is
	// open. Before the fix the bytes exist, but in the wrapper's log file.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()

	if err := ps.waitForText(ctx2, "Select branch to checkout"); err != nil {
		t.Fatalf("issue #124: branch selector never reached the terminal. The prompt "+
			"is live but invisible because wt() redirected stdout to a file.\n"+
			"err: %v\nTerminal saw:\n%s", err, ps.getOutput())
	}

	// Cancel the prompt so no worktree is left behind.
	ps.send("\x03")
	time.Sleep(500 * time.Millisecond)
}

// TestNavigationMarkerStaysOnStdout is the other half of the fix: prompts moved
// to stderr, but the "wt navigating to:" marker must stay on stdout or the
// wrapper's grep would stop finding it and auto-cd would silently break.
func TestNavigationMarkerStaysOnStdout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	tmpDir := t.TempDir()
	repoDir := filepath.Join(tmpDir, "test-repo")
	worktreeRoot := filepath.Join(tmpDir, "worktrees")

	setupTestRepo(t, repoDir)
	wtBinary := buildWtBinary(t, tmpDir)
	runGitCommand(t, repoDir, "branch", "feature-1")

	cmd := exec.Command(wtBinary, "checkout", "feature-1")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "WORKTREE_ROOT="+worktreeRoot)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("wt checkout failed: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}

	const marker = "wt navigating to: "
	if !strings.Contains(stdout.String(), marker) {
		t.Errorf("navigation marker missing from stdout; the shell integration "+
			"greps stdout for it, so auto-cd would break.\nstdout: %s\nstderr: %s",
			&stdout, &stderr)
	}
}

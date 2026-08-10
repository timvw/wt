package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests execute the generated bash wrapper for real, against a fake wt on
// PATH. They cover the two Git Bash-specific branches added for #112 — the
// no-script(1) fallback and the cygpath path translation — on any OS, because
// both branches are reachable from an ordinary bash by controlling what the
// wrapper finds when it probes for those two commands.

// writeShellenvBash renders the bash/zsh integration script to a file and
// returns its path. writeBashZshShellenv writes to os.Stdout, so stdout is
// redirected for the duration of the call.
func writeShellenvBash(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "shellenv.bash")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating shellenv file: %v", err)
	}

	orig := os.Stdout
	os.Stdout = f
	writeBashZshShellenv()
	os.Stdout = orig

	if err := f.Close(); err != nil {
		t.Fatalf("closing shellenv file: %v", err)
	}
	return path
}

// writeExecutable writes a script and makes it executable.
func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// fakeWtScript stands in for the real binary: it prints a line of ordinary
// output, then the navigation marker the wrapper looks for, then exits with a
// caller-controlled status.
const fakeWtScript = `#!/bin/sh
echo "fake wt ran: $*"
if [ -n "${FAKE_WT_TARGET:-}" ]; then
    echo "wt navigating to: ${FAKE_WT_TARGET}"
fi
exit "${FAKE_WT_EXIT:-0}"
`

// runBashWrapper sources the generated wrapper and runs `wt checkout demo`
// through it, starting from startDir. The `command` override makes the
// wrapper's `command -v script` probe fail, which is how Git Bash behaves:
// it ships no script(1). Without the override the test would take whichever
// branch the host happens to have.
//
// It returns the wrapper's exit status, the resulting working directory, and
// the combined output.
func runBashWrapper(t *testing.T, binDir, shellenv, startDir string, env []string) (exitCode int, cwd, output string) {
	t.Helper()

	script := fmt.Sprintf(`
export PATH=%q:"$PATH"
command() {
    if [ "$1" = "-v" ] && [ "$2" = "script" ]; then
        return 1
    fi
    builtin command "$@"
}
. %q
cd %q
wt checkout demo
echo "__EXIT__=$?"
echo "__PWD__=$(pwd)"
`, binDir, shellenv, startDir)

	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// The harness itself always exits 0; the wrapper's status is echoed.
		t.Fatalf("running bash harness: %v\noutput:\n%s", err, out)
	}
	output = string(out)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "__EXIT__="):
			if _, err := fmt.Sscanf(line, "__EXIT__=%d", &exitCode); err != nil {
				t.Fatalf("parsing %q: %v", line, err)
			}
		case strings.HasPrefix(line, "__PWD__="):
			cwd = strings.TrimPrefix(line, "__PWD__=")
		}
	}
	if cwd == "" {
		t.Fatalf("harness did not report a working directory:\n%s", output)
	}
	return exitCode, cwd, output
}

// realPath resolves symlinks so comparisons survive macOS's /var -> /private/var.
func realPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolving %s: %v", path, err)
	}
	return resolved
}

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
}

// TestBashWrapperAutoCdWithoutScript covers the fallback taken when script(1)
// is missing, as it is in Git Bash (#112). Auto-cd must still work, and the
// command's output must still reach the terminal even though stdout was
// redirected to the log file.
func TestBashWrapperAutoCdWithoutScript(t *testing.T) {
	requireBash(t)

	dir := t.TempDir()
	shellenv := writeShellenvBash(t, dir)

	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "wt"), fakeWtScript)

	target := filepath.Join(dir, "worktrees", "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	exitCode, cwd, output := runBashWrapper(t, binDir, shellenv, dir, []string{
		"FAKE_WT_TARGET=" + target,
	})

	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0\noutput:\n%s", exitCode, output)
	}
	if got, want := realPath(t, cwd), realPath(t, target); got != want {
		t.Errorf("wrapper did not auto-cd: cwd = %q, want %q\noutput:\n%s", got, want, output)
	}
	// The fallback redirects stdout to the log file, so it has to replay it.
	if !strings.Contains(output, "fake wt ran: checkout demo") {
		t.Errorf("command output was swallowed by the fallback:\n%s", output)
	}
}

// TestBashWrapperExitCodeWithoutScript pins the reason the fallback redirects
// instead of piping through tee: a pipeline would report tee's status, so a
// failing command would look successful and the wrapper would cd anyway.
func TestBashWrapperExitCodeWithoutScript(t *testing.T) {
	requireBash(t)

	dir := t.TempDir()
	shellenv := writeShellenvBash(t, dir)

	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "wt"), fakeWtScript)

	target := filepath.Join(dir, "worktrees", "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	exitCode, cwd, output := runBashWrapper(t, binDir, shellenv, dir, []string{
		"FAKE_WT_TARGET=" + target,
		"FAKE_WT_EXIT=3",
	})

	if exitCode != 3 {
		t.Errorf("exit code = %d, want 3 (the failing command's status)\noutput:\n%s", exitCode, output)
	}
	if got, want := realPath(t, cwd), realPath(t, dir); got != want {
		t.Errorf("wrapper cd'd despite a failing command: cwd = %q, want %q\noutput:\n%s", got, want, output)
	}
}

// TestBashWrapperTranslatesNativePathWithCygpath covers the other half of the
// Git Bash fix: wt.exe is a native binary and prints native paths (C:\...),
// which bash's cd cannot use. When cygpath is present the wrapper must run the
// path through it. A stub cygpath stands in so this runs anywhere.
func TestBashWrapperTranslatesNativePathWithCygpath(t *testing.T) {
	requireBash(t)

	dir := t.TempDir()
	shellenv := writeShellenvBash(t, dir)

	binDir := filepath.Join(dir, "bin")
	if err := os.Mkdir(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "wt"), fakeWtScript)

	target := filepath.Join(dir, "worktrees", "demo")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	// Stub cygpath: translates the one native path this test uses, and fails
	// on anything else so a wrapper that passed the wrong argument is caught.
	nativePath := `C:\worktrees\demo`
	writeExecutable(t, filepath.Join(binDir, "cygpath"), fmt.Sprintf(`#!/bin/sh
if [ "$1" = "-u" ] && [ "$2" = %q ]; then
    echo %q
    exit 0
fi
echo "cygpath: unexpected arguments: $*" >&2
exit 1
`, nativePath, target))

	exitCode, cwd, output := runBashWrapper(t, binDir, shellenv, dir, []string{
		"FAKE_WT_TARGET=" + nativePath,
	})

	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0\noutput:\n%s", exitCode, output)
	}
	if got, want := realPath(t, cwd), realPath(t, target); got != want {
		t.Errorf("native path was not translated: cwd = %q, want %q\noutput:\n%s", got, want, output)
	}
}

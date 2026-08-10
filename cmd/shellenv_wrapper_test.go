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
// Where the wrapper ended up is reported by probing for marker files rather
// than by comparing paths: under Git Bash the interpreter names the same
// directory /tmp/... that Go names C:\Users\...\Temp\..., so no string (or
// even EvalSymlinks) comparison of the two holds.
//
// It returns the wrapper's exit status, which marker directory it landed in,
// and the combined output.
func runBashWrapper(t *testing.T, binDir, shellenv, startDir, targetDir string, env []string) (exitCode int, landed, output string) {
	t.Helper()

	// Paths are handed over in the host's native form and converted inside the
	// shell: under Git Bash a native path in PATH would be split on its
	// drive-letter colon.
	script := fmt.Sprintf(`
to_posix() {
    if command -v cygpath >/dev/null 2>&1; then cygpath -u "$1"; else printf '%%s' "$1"; fi
}
WT_TEST_BIN=$(to_posix %q)
WT_TEST_SHELLENV=$(to_posix %q)
WT_TEST_START=$(to_posix %q)
# Exported for the stub cygpath, which cannot compute this itself.
export WT_TEST_TARGET_POSIX=$(to_posix %q)
export PATH="$WT_TEST_BIN:$PATH"
command() {
    if [ "$1" = "-v" ] && [ "$2" = "script" ]; then
        return 1
    fi
    builtin command "$@"
}
. "$WT_TEST_SHELLENV"
cd "$WT_TEST_START"
wt checkout demo
echo "__EXIT__=$?"
for marker in start target; do
    if [ -f ".wt-test-$marker" ]; then
        echo "__LANDED__=$marker"
    fi
done
echo "__PWD__=$(pwd)"
`, binDir, shellenv, startDir, targetDir)

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
		case strings.HasPrefix(line, "__LANDED__="):
			landed = strings.TrimPrefix(line, "__LANDED__=")
		}
	}
	if landed == "" {
		t.Fatalf("wrapper ended up in a directory with no marker:\n%s", output)
	}
	return exitCode, landed, output
}

// stageWrapperDirs lays out a fixture: a bin directory holding the fake wt, a
// start directory, and a target directory to navigate to. Each directory gets
// a marker file so the wrapper's final location can be identified without
// comparing path strings.
func stageWrapperDirs(t *testing.T) (binDir, startDir, targetDir string) {
	t.Helper()

	root := t.TempDir()
	binDir = filepath.Join(root, "bin")
	startDir = filepath.Join(root, "start")
	targetDir = filepath.Join(root, "worktrees", "demo")

	for _, dir := range []string{binDir, startDir, targetDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	writeExecutable(t, filepath.Join(binDir, "wt"), fakeWtScript)
	for dir, marker := range map[string]string{startDir: "start", targetDir: "target"} {
		if err := os.WriteFile(filepath.Join(dir, ".wt-test-"+marker), nil, 0o644); err != nil {
			t.Fatalf("writing marker in %s: %v", dir, err)
		}
	}

	return binDir, startDir, targetDir
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

	binDir, startDir, targetDir := stageWrapperDirs(t)
	shellenv := writeShellenvBash(t, t.TempDir())

	exitCode, landed, output := runBashWrapper(t, binDir, shellenv, startDir, targetDir, []string{
		"FAKE_WT_TARGET=" + targetDir,
	})

	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0\noutput:\n%s", exitCode, output)
	}
	if landed != "target" {
		t.Errorf("wrapper did not auto-cd: ended up in the %q directory\noutput:\n%s", landed, output)
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

	binDir, startDir, targetDir := stageWrapperDirs(t)
	shellenv := writeShellenvBash(t, t.TempDir())

	exitCode, landed, output := runBashWrapper(t, binDir, shellenv, startDir, targetDir, []string{
		"FAKE_WT_TARGET=" + targetDir,
		"FAKE_WT_EXIT=3",
	})

	if exitCode != 3 {
		t.Errorf("exit code = %d, want 3 (the failing command's status)\noutput:\n%s", exitCode, output)
	}
	if landed != "start" {
		t.Errorf("wrapper cd'd despite a failing command: ended up in the %q directory\noutput:\n%s", landed, output)
	}
}

// TestBashWrapperTranslatesNativePathWithCygpath covers the other half of the
// Git Bash fix: wt.exe is a native binary and prints native paths (C:\...),
// which bash's cd cannot use. When cygpath is present the wrapper must run the
// path through it. A stub cygpath stands in so this runs anywhere.
func TestBashWrapperTranslatesNativePathWithCygpath(t *testing.T) {
	requireBash(t)

	binDir, startDir, targetDir := stageWrapperDirs(t)
	shellenv := writeShellenvBash(t, t.TempDir())

	// Stub cygpath, first on PATH so it shadows a real one. It translates the
	// single native path this test uses and fails on anything else, so a
	// wrapper that passed the wrong argument is caught rather than ignored.
	// The POSIX form of the target comes from the harness, which is the only
	// place that can compute it on either kind of host.
	nativePath := `C:\wt-test\worktrees\demo`
	writeExecutable(t, filepath.Join(binDir, "cygpath"), fmt.Sprintf(`#!/bin/sh
if [ "$1" = "-u" ] && [ "$2" = %q ]; then
    printf '%%s\n' "$WT_TEST_TARGET_POSIX"
    exit 0
fi
echo "cygpath: unexpected arguments: $*" >&2
exit 1
`, nativePath))

	exitCode, landed, output := runBashWrapper(t, binDir, shellenv, startDir, targetDir, []string{
		"FAKE_WT_TARGET=" + nativePath,
	})

	if exitCode != 0 {
		t.Errorf("exit code = %d, want 0\noutput:\n%s", exitCode, output)
	}
	if landed != "target" {
		t.Errorf("native path was not translated: ended up in the %q directory\noutput:\n%s", landed, output)
	}
}

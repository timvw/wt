package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests execute the generated fish wrapper for real, against a fake wt on
// PATH, covering the no-script(1) fallback added for #131. The bash equivalents
// live in shellenv_wrapper_test.go and share the fixtures below.

// fishFallbackPathTools are the external commands the fish wrapper needs on the
// no-script(1) path. PATH is narrowed to symlinks of exactly these (plus the
// fake wt), which is how the fallback is forced on a host that does have
// script(1): nothing else can make `type -q script` fail from the outside.
var fishFallbackPathTools = []string{"mktemp", "cat", "grep", "tail", "sed", "rm"}

// writeShellenvFish renders the fish integration script to a file and returns
// its path.
func writeShellenvFish(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "shellenv.fish")
	if err := os.WriteFile(path, []byte(fishShellenvScript()), 0o644); err != nil {
		t.Fatalf("writing shellenv file: %v", err)
	}
	return path
}

// linkFallbackTools symlinks the commands in fishFallbackPathTools into binDir
// so PATH can be narrowed to it. A tool missing from the host skips the test
// rather than silently changing what is exercised.
func linkFallbackTools(t *testing.T, binDir string) {
	t.Helper()

	for _, tool := range fishFallbackPathTools {
		src, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
		if err := os.Symlink(src, filepath.Join(binDir, tool)); err != nil {
			t.Fatalf("linking %s: %v", tool, err)
		}
	}
}

// runFishWrapper sources the generated wrapper and runs `wt checkout demo`
// through it, starting from startDir. It returns the wrapper's exit status,
// which marker directory it landed in, and the combined output.
//
// Config loading is disabled by pointing HOME and XDG_CONFIG_HOME at a scratch
// directory: a developer's own config.fish must not decide what this asserts.
func runFishWrapper(t *testing.T, binDir, shellenv, startDir, targetDir string, env []string) (exitCode int, landed, output string) {
	t.Helper()

	script := `
set -gx PATH $WT_TEST_BIN
if type -q script
    echo "__PROBE__=found"
end
source $WT_TEST_SHELLENV
cd $WT_TEST_START
wt checkout demo
echo "__EXIT__=$status"
for marker in start target
    if test -f ".wt-test-$marker"
        echo "__LANDED__=$marker"
    end
end
echo "__PWD__="(pwd)
`

	cmd := exec.Command("fish", "-c", script)
	cmd.Env = append(os.Environ(),
		"WT_TEST_BIN="+binDir,
		"WT_TEST_SHELLENV="+shellenv,
		"WT_TEST_START="+startDir,
		"HOME="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir(),
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// The harness itself always exits 0; the wrapper's status is echoed.
		t.Fatalf("running fish harness: %v\noutput:\n%s", err, out)
	}
	output = string(out)

	// The probe the wrapper itself makes, run under the same PATH. If it finds
	// a script(1) after all, the run took a different branch and every
	// assertion about the fallback below would hold vacuously.
	if strings.Contains(output, "__PROBE__=found") {
		t.Fatalf("script(1) is still reachable, so the fallback was not exercised:\n%s", output)
	}

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

func requireFish(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish not available")
	}
}

// stageFishWrapperDirs lays out the shared fixture and additionally narrows
// what the wrapper can find on PATH to the fallback's toolset.
func stageFishWrapperDirs(t *testing.T) (binDir, startDir, targetDir string) {
	t.Helper()

	binDir, startDir, targetDir = stageWrapperDirs(t)
	linkFallbackTools(t, binDir)
	return binDir, startDir, targetDir
}

// TestFishWrapperAutoCdWithoutScript covers the fallback taken when script(1)
// is missing (#131). Before it existed the wrapper did not degrade at all: the
// absent command was the one meant to produce the output, so every wt call
// failed. Auto-cd must work and the output must still reach the terminal even
// though stdout was redirected to the log file.
func TestFishWrapperAutoCdWithoutScript(t *testing.T) {
	requireFish(t)

	binDir, startDir, targetDir := stageFishWrapperDirs(t)
	shellenv := writeShellenvFish(t, t.TempDir())

	exitCode, landed, output := runFishWrapper(t, binDir, shellenv, startDir, targetDir, []string{
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

// TestFishWrapperExitCodeWithoutScript pins the reason the fallback captures
// $status straight after the command rather than at the end of the branch: the
// replaying `cat` would otherwise overwrite it, so a failing command would look
// successful and the wrapper would cd anyway.
func TestFishWrapperExitCodeWithoutScript(t *testing.T) {
	requireFish(t)

	binDir, startDir, targetDir := stageFishWrapperDirs(t)
	shellenv := writeShellenvFish(t, t.TempDir())

	exitCode, landed, output := runFishWrapper(t, binDir, shellenv, startDir, targetDir, []string{
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

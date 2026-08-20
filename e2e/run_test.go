package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The runner used to locate the binary under test by scanning for ./bin/wt and
// then falling back to whatever wt was on PATH. Both let a run silently
// exercise a binary unrelated to the source tree: bin/ is gitignored so a
// stale build survives a rebase, and a system-wide install is never what the
// suite means to test. See issue #141.

func TestResolveWtBinaryBuildsFromWorkingTree(t *testing.T) {
	binary, cleanup, err := resolveWtBinary("")
	if err != nil {
		t.Fatalf("resolveWtBinary(\"\") returned error: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(binary); err != nil {
		t.Fatalf("built binary %s is not present: %v", binary, err)
	}
	if !filepath.IsAbs(binary) {
		t.Errorf("built binary path %q is not absolute", binary)
	}

	// It must be a freshly built wt, not something found lying around.
	out, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("running built binary failed: %v\n%s", err, out)
	}

	cleanup()
	if _, err := os.Stat(binary); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove the build directory, stat returned %v", err)
	}
}

func TestResolveWtBinaryIgnoresWtOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script decoy is not executable on Windows")
	}

	// A wt on PATH that the old fallback would have preferred over building.
	// This is the mode that produced the most misleading result in #141: a
	// released, system-wide install being reported on as if it were the
	// working tree.
	decoyDir := t.TempDir()
	decoy := filepath.Join(decoyDir, "wt")
	if err := os.WriteFile(decoy, []byte("#!/bin/sh\necho DECOY\n"), 0o755); err != nil {
		t.Fatalf("write PATH decoy: %v", err)
	}
	t.Setenv("PATH", decoyDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	binary, cleanup, err := resolveWtBinary("")
	if err != nil {
		t.Fatalf("resolveWtBinary(\"\") returned error: %v", err)
	}
	defer cleanup()

	if binary == decoy {
		t.Fatal("resolveWtBinary selected the wt on PATH instead of building")
	}

	out, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("running built binary failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "DECOY") {
		t.Errorf("resolveWtBinary returned the decoy binary, output: %s", out)
	}
}

func TestResolveWtBinaryIgnoresStaleBinArtifact(t *testing.T) {
	// The other half of #141: bin/ is gitignored, so a binary built before a
	// rebase stays behind and the old candidate scan preferred it over the
	// current source.
	//
	// The decoy lives inside the module, because resolving the module root
	// reads the working directory.
	root, err := moduleRoot()
	if err != nil {
		t.Fatalf("moduleRoot: %v", err)
	}
	workDir, err := os.MkdirTemp(root, "stale-artifact-test")
	if err != nil {
		t.Fatalf("create work directory: %v", err)
	}
	// Registered before the t.Chdir below so it runs after it: cleanups are
	// LIFO, and removing the working directory fails on Windows. Otherwise
	// this leaves directories behind in the repository root.
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	name := "wt"
	if runtime.GOOS == "windows" {
		name = "wt.exe"
	}
	if err := os.Mkdir(filepath.Join(workDir, "bin"), 0o755); err != nil {
		t.Fatalf("create bin directory: %v", err)
	}
	decoy := filepath.Join(workDir, "bin", name)
	if err := os.WriteFile(decoy, []byte("stale"), 0o755); err != nil {
		t.Fatalf("write stale artifact: %v", err)
	}

	t.Chdir(workDir)

	binary, cleanup, err := resolveWtBinary("")
	if err != nil {
		t.Fatalf("resolveWtBinary(\"\") returned error: %v", err)
	}
	defer cleanup()

	if binary == decoy {
		t.Fatal("resolveWtBinary selected the stale bin/ artifact instead of building")
	}
	// A real build, not the placeholder contents.
	if out, err := exec.Command(binary, "version").CombinedOutput(); err != nil {
		t.Fatalf("running built binary failed: %v\n%s", err, out)
	}
}

func TestResolveWtBinaryRejectsMissingExplicitPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	if _, _, err := resolveWtBinary(missing); err == nil {
		t.Fatal("resolveWtBinary accepted a -wt path that does not exist; it must not silently fall back")
	}
}

func TestResolveWtBinaryRejectsExplicitDirectory(t *testing.T) {
	if _, _, err := resolveWtBinary(t.TempDir()); err == nil {
		t.Fatal("resolveWtBinary accepted a directory as -wt; it must fail before running any scenario")
	}
}

func TestResolveWtBinaryUsesExplicitPath(t *testing.T) {
	dir := t.TempDir()
	name := "wt"
	if runtime.GOOS == "windows" {
		name = "wt.exe"
	}
	explicit := filepath.Join(dir, name)
	// Never executed here, only resolved, so the contents do not matter.
	if err := os.WriteFile(explicit, nil, 0o755); err != nil {
		t.Fatalf("write explicit binary: %v", err)
	}

	binary, cleanup, err := resolveWtBinary(explicit)
	if err != nil {
		t.Fatalf("resolveWtBinary(%q) returned error: %v", explicit, err)
	}
	defer cleanup()

	if binary != explicit {
		t.Errorf("resolveWtBinary(%q) = %q, want the path as given", explicit, binary)
	}
}

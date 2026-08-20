//go:build unix

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// CopyFile refuses a FIFO, socket or device node, so --dry-run must not promise
// a copy the real run will report as a failure.
func TestDryRunPredictsTheFailureOnASpecialFile(t *testing.T) {
	// git ls-files never reports a special file, so it has to be reached the
	// way the planner reaches one: inside a directory selected whole.
	src := newFilesRepo(t, "run/\n")
	if err := os.MkdirAll(filepath.Join(src, "run"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := unix.Mkfifo(filepath.Join(src, "run", "daemon.sock"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	dst := newDestination(t)
	setFileConfig(t, []string{"run/"}, nil, nil, false)

	dry, err := runFileCopy(src, dst, copyOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	real, err := runFileCopy(src, dst, copyOptions{})
	if err != nil {
		t.Fatalf("real run: %v", err)
	}

	if len(dry.Files) != 1 || len(real.Files) != 1 {
		t.Fatalf("dry %+v, real %+v; want one entry each", dry.Files, real.Files)
	}
	if dry.Files[0].Action != fileActionFailed {
		t.Errorf("dry run action = %q, want %q", dry.Files[0].Action, fileActionFailed)
	}
	if dry.Files[0].Action != real.Files[0].Action {
		t.Errorf("dry %+v, real %+v", dry.Files[0], real.Files[0])
	}
	if _, err := os.Lstat(filepath.Join(dst, "run", "daemon.sock")); !os.IsNotExist(err) {
		t.Error("the special file was copied into the destination")
	}
}

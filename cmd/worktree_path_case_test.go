package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindCaseInsensitivePathCollisionNestedBranchPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	existing := filepath.Join(tmpDir, "repo", "feature")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatalf("failed to create existing path: %v", err)
	}

	candidate := filepath.Join(tmpDir, "repo", "Feature", "make-it-work")
	got, ok := findCaseInsensitivePathCollision(candidate)
	if !ok {
		t.Fatal("expected case-insensitive path collision")
	}
	if got != existing {
		t.Fatalf("collision path = %q, want %q", got, existing)
	}
}

func TestFindCaseInsensitivePathCollisionFlatDifferentBranches(t *testing.T) {
	tmpDir := t.TempDir()
	existing := filepath.Join(tmpDir, "repo", "feature-add-logging")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatalf("failed to create existing path: %v", err)
	}

	candidate := filepath.Join(tmpDir, "repo", "Feature-make-it-work")
	if got, ok := findCaseInsensitivePathCollision(candidate); ok {
		t.Fatalf("unexpected collision with %q", got)
	}
}

func TestFindCaseInsensitivePathCollisionCaseOnlyFlatBranch(t *testing.T) {
	tmpDir := t.TempDir()
	existing := filepath.Join(tmpDir, "repo", "feature-make-it-work")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatalf("failed to create existing path: %v", err)
	}

	candidate := filepath.Join(tmpDir, "repo", "Feature-make-it-work")
	got, ok := findCaseInsensitivePathCollision(candidate)
	if !ok {
		t.Fatal("expected case-only flat path collision")
	}
	if got != existing {
		t.Fatalf("collision path = %q, want %q", got, existing)
	}
}

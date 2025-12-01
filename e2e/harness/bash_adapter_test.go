package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBashAdapterBasicCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping bash adapter test in short mode")
	}

	adapter := NewBashAdapter()

	// Create a temporary directory structure
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a fake wt script
	wtScript := filepath.Join(tmpDir, "wt")
	scriptContent := `#!/bin/bash
	echo "wt() { command wt \"\$@\"; }"
	`
	if err := os.WriteFile(wtScript, []byte(scriptContent), 0755); err != nil {
		t.Fatal(err)
	}

	// Setup adapter with minimal environment
	err := adapter.Setup(wtScript, tmpDir, testDir)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	defer adapter.Cleanup()

	// Test 1: Execute echo command
	t.Run("execute echo", func(t *testing.T) {
		result, err := adapter.Execute("echo", []string{"hello", "world"})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("Exit code = %d, want 0", result.ExitCode)
		}

		if !contains(result.Stdout, "hello world") {
			t.Errorf("Stdout does not contain 'hello world': %q", result.Stdout)
		}
	})

	// Test 2: Get pwd
	t.Run("get pwd", func(t *testing.T) {
		pwd, err := adapter.GetPwd()
		if err != nil {
			t.Fatalf("GetPwd failed: %v", err)
		}

		if pwd == "" {
			t.Error("GetPwd returned empty string")
		}

		if !contains(pwd, "test") {
			t.Errorf("Pwd does not contain 'test': %q", pwd)
		}
	})

	// Test 3: Execute command with non-zero exit code
	t.Run("non-zero exit code", func(t *testing.T) {
		result, err := adapter.Execute("false", nil)
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if result.ExitCode == 0 {
			t.Error("Exit code = 0, want non-zero")
		}
	})

	// Test 4: cd command changes pwd
	t.Run("cd changes pwd", func(t *testing.T) {
		// Get initial pwd
		pwd1, err := adapter.GetPwd()
		if err != nil {
			t.Fatalf("GetPwd failed: %v", err)
		}

		// cd to parent
		result, err := adapter.Execute("cd", []string{".."})
		if err != nil {
			t.Fatalf("Execute cd failed: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("cd exit code = %d, want 0", result.ExitCode)
		}

		// Get new pwd
		pwd2, err := adapter.GetPwd()
		if err != nil {
			t.Fatalf("GetPwd failed: %v", err)
		}

		if pwd1 == pwd2 {
			t.Errorf("Pwd did not change after cd: %q", pwd2)
		}
	})
}

func TestBashAdapterName(t *testing.T) {
	adapter := NewBashAdapter()
	if adapter.Name() != "bash" {
		t.Errorf("Name() = %q, want %q", adapter.Name(), "bash")
	}
}

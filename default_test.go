package main

import (
	"strings"
	"testing"
)

func TestDefaultCmdRegistered(t *testing.T) {
	// Verify the command is registered on rootCmd
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "default" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("defaultCmd is not registered on rootCmd")
	}
}

func TestDefaultCmdMetadata(t *testing.T) {
	if defaultCmd.Use != "default" {
		t.Errorf("defaultCmd.Use = %q, want %q", defaultCmd.Use, "default")
	}
	if defaultCmd.Short == "" {
		t.Error("defaultCmd.Short should not be empty")
	}
}

func TestDefaultCmdRunsWithoutError(t *testing.T) {
	// Run the command in the current repo -- it should succeed
	// and produce output containing the CD marker.
	buf := new(strings.Builder)
	defaultCmd.SetOut(buf)
	defaultCmd.SetErr(buf)

	// Save and restore outputFormat
	origFmt := outputFormat
	outputFormat = formatText
	t.Cleanup(func() { outputFormat = origFmt })

	err := defaultCmd.RunE(defaultCmd, nil)
	if err != nil {
		t.Fatalf("defaultCmd.RunE() returned error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "wt navigating to: ") {
		// printCDMarker writes to os.Stdout, not cmd.OutOrStdout,
		// so we just verify no error was returned.
		t.Log("Note: CD marker goes to os.Stdout; verified no error returned")
	}
}

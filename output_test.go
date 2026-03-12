package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestValidateOutputFormat(t *testing.T) {
	original := outputFormat
	t.Cleanup(func() { outputFormat = original })

	outputFormat = "JSON"
	if err := validateOutputFormat(); err != nil {
		t.Fatalf("validateOutputFormat() unexpected error: %v", err)
	}
	if outputFormat != "json" {
		t.Fatalf("validateOutputFormat() normalized format = %q, want %q", outputFormat, "json")
	}

	outputFormat = "yaml"
	if err := validateOutputFormat(); err == nil {
		t.Fatal("validateOutputFormat() expected error for unsupported format")
	}
}

func TestPrintCDMarkerSkipsJSONOutput(t *testing.T) {
	original := outputFormat
	t.Cleanup(func() { outputFormat = original })

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	outputFormat = "json"
	printCDMarker("/tmp/worktree")

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read stdout: %v", err)
	}

	if strings.TrimSpace(buf.String()) != "" {
		t.Fatalf("expected no output in json mode, got %q", buf.String())
	}
}

func TestEmitJSONSuccess(t *testing.T) {
	original := outputFormat
	t.Cleanup(func() { outputFormat = original })
	outputFormat = "json"

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	err = emitJSONSuccess(rootCmd, map[string]string{"hello": "world"})
	if err != nil {
		t.Fatalf("emitJSONSuccess() unexpected error: %v", err)
	}

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read stdout: %v", err)
	}

	var payload struct {
		OK      bool              `json:"ok"`
		Command string            `json:"command"`
		Data    map[string]string `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}
	if !payload.OK {
		t.Fatalf("expected ok=true, got false")
	}
	if payload.Command != "wt" {
		t.Fatalf("expected command wt, got %q", payload.Command)
	}
	if payload.Data["hello"] != "world" {
		t.Fatalf("expected data.hello=world, got %q", payload.Data["hello"])
	}
}

func TestRootHelpUsesJSONFormat(t *testing.T) {
	original := outputFormat
	t.Cleanup(func() { outputFormat = original })
	outputFormat = "json"

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	if err := rootCmd.Help(); err != nil {
		t.Fatalf("help returned error: %v", err)
	}

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read stdout: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `"ok":true`) {
		t.Fatalf("expected JSON help envelope, got: %s", out)
	}
}

func TestConfigHelpUsesJSONFormat(t *testing.T) {
	original := outputFormat
	t.Cleanup(func() { outputFormat = original })
	outputFormat = "json"

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })

	if err := configCmd.Help(); err != nil {
		t.Fatalf("help returned error: %v", err)
	}

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read stdout: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `"ok":true`) {
		t.Fatalf("expected JSON help envelope, got: %s", out)
	}
	if !strings.Contains(out, `"command":"wt config"`) {
		t.Fatalf("expected wt config command in JSON, got: %s", out)
	}
}

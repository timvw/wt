package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestParseAheadBehind(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAhead  int
		wantBehind int
		wantErr    bool
	}{
		{
			name:       "Zero ahead and behind",
			input:      "0\t0\n",
			wantAhead:  0,
			wantBehind: 0,
		},
		{
			name:       "Ahead only",
			input:      "3\t0\n",
			wantAhead:  3,
			wantBehind: 0,
		},
		{
			name:       "Behind only",
			input:      "0\t5\n",
			wantAhead:  0,
			wantBehind: 5,
		},
		{
			name:       "Both ahead and behind",
			input:      "2\t1\n",
			wantAhead:  2,
			wantBehind: 1,
		},
		{
			name:       "No trailing newline",
			input:      "4\t7",
			wantAhead:  4,
			wantBehind: 7,
		},
		{
			name:       "Large numbers",
			input:      "100\t200\n",
			wantAhead:  100,
			wantBehind: 200,
		},
		{
			name:    "Empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "Only whitespace",
			input:   "   \n",
			wantErr: true,
		},
		{
			name:    "Single number",
			input:   "5\n",
			wantErr: true,
		},
		{
			name:    "Non-numeric",
			input:   "abc\tdef\n",
			wantErr: true,
		},
		{
			name:    "Three columns",
			input:   "1\t2\t3\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ahead, behind, err := parseAheadBehind(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseAheadBehind(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if ahead != tt.wantAhead {
					t.Errorf("parseAheadBehind(%q) ahead = %d, want %d", tt.input, ahead, tt.wantAhead)
				}
				if behind != tt.wantBehind {
					t.Errorf("parseAheadBehind(%q) behind = %d, want %d", tt.input, behind, tt.wantBehind)
				}
			}
		})
	}
}

func TestIsDirty(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "Empty output means clean",
			input: "",
			want:  false,
		},
		{
			name:  "Whitespace only means clean",
			input: "   \n  \n",
			want:  false,
		},
		{
			name:  "Modified file",
			input: " M main.go\n",
			want:  true,
		},
		{
			name:  "Added file",
			input: "A  new_file.go\n",
			want:  true,
		},
		{
			name:  "Untracked file",
			input: "?? untracked.go\n",
			want:  true,
		},
		{
			name:  "Multiple changes",
			input: " M main.go\n?? new.go\nD  old.go\n",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDirtyStatus(tt.input)
			if got != tt.want {
				t.Errorf("isDirtyStatus(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestStatusCommandRegistered(t *testing.T) {
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "status" {
			found = true
			break
		}
	}
	if !found {
		t.Error("statusCmd is not registered on rootCmd")
	}
}

func TestFormatStatusLine(t *testing.T) {
	tests := []struct {
		name  string
		entry worktreeStatus
		want  string
	}{
		{
			name: "Current worktree dirty with ahead/behind",
			entry: worktreeStatus{
				Path:        "/path/to/worktree",
				Branch:      "feat/foo",
				HEAD:        "abc1234",
				Dirty:       true,
				Ahead:       2,
				Behind:      1,
				Current:     true,
				HasUpstream: true,
			},
			want: "* feat/foo",
		},
		{
			name: "Non-current clean worktree",
			entry: worktreeStatus{
				Path:        "/path/to/main",
				Branch:      "main",
				HEAD:        "def5678",
				Dirty:       false,
				Ahead:       0,
				Behind:      0,
				Current:     false,
				HasUpstream: true,
			},
			want: "  main",
		},
		{
			name: "No upstream",
			entry: worktreeStatus{
				Path:        "/path/to/wt",
				Branch:      "fix/bar",
				HEAD:        "ghi9012",
				Dirty:       false,
				Ahead:       0,
				Behind:      0,
				Current:     false,
				HasUpstream: false,
			},
			want: "  fix/bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatStatusLine(tt.entry)
			if !strings.Contains(got, tt.want) {
				t.Errorf("formatStatusLine() = %q, want it to contain %q", got, tt.want)
			}
			if tt.entry.Dirty {
				if !strings.Contains(got, "dirty") {
					t.Errorf("formatStatusLine() = %q, want it to contain 'dirty'", got)
				}
			} else {
				if !strings.Contains(got, "clean") {
					t.Errorf("formatStatusLine() = %q, want it to contain 'clean'", got)
				}
			}
			if tt.entry.HasUpstream {
				if !strings.Contains(got, "↑") || !strings.Contains(got, "↓") {
					t.Errorf("formatStatusLine() = %q, want it to contain ahead/behind arrows", got)
				}
			} else {
				if !strings.Contains(got, "no upstream") {
					t.Errorf("formatStatusLine() = %q, want it to contain 'no upstream'", got)
				}
			}
		})
	}
}

func TestFormatCIColor(t *testing.T) {
	tests := []struct {
		ci   string
		want string
	}{
		{"pass", "✓ CI"},
		{"fail", "✗ CI"},
		{"pending", "● CI"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.ci, func(t *testing.T) {
			got := formatCIColor(tt.ci)
			// Strip ANSI codes for content check
			stripped := stripAnsi(got)
			if stripped != tt.want {
				t.Errorf("formatCIColor(%q) stripped = %q, want %q", tt.ci, stripped, tt.want)
			}
		})
	}
}

func stripAnsi(s string) string {
	result := s
	for {
		start := strings.Index(result, "\033[")
		if start == -1 {
			break
		}
		end := strings.Index(result[start:], "m")
		if end == -1 {
			break
		}
		result = result[:start] + result[start+end+1:]
	}
	return result
}

func TestNormalizeGitHubState(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"success", "pass"},
		{"failure", "fail"},
		{"error", "fail"},
		{"pending", "pending"},
		{"", ""},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeGitHubState(tt.input); got != tt.want {
				t.Errorf("normalizeGitHubState(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeGitHubCheckRuns(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"success", "pass"},
		{"success,success", "pass"},
		{"failure", "fail"},
		{"success,failure", "fail"},
		{"timed_out", "fail"},
		{"cancelled", "fail"},
		{"", "pending"},
		{"null", "pending"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeGitHubCheckRuns(tt.input); got != tt.want {
				t.Errorf("normalizeGitHubCheckRuns(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeGitLabState(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`{"status":"success"}`, "pass"},
		{`{"status":"failed"}`, "fail"},
		{`{"status":"running"}`, "pending"},
		{`{"status":"pending"}`, "pending"},
		{`{"status":"canceled"}`, ""},
		{`{"status":"skipped"}`, ""},
		{`invalid json`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := normalizeGitLabState(tt.input); got != tt.want {
				t.Errorf("normalizeGitLabState(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatStatusLineWithCI(t *testing.T) {
	st := worktreeStatus{
		Path:        "/path/to/worktree",
		Branch:      "feat/foo",
		Dirty:       false,
		Current:     false,
		HasUpstream: true,
		CI:          "pass",
	}
	got := formatStatusLine(st)
	if !strings.Contains(got, "pass") {
		t.Errorf("formatStatusLine() = %q, expected it to contain CI status 'pass'", got)
	}

	// Without CI
	st.CI = ""
	got = formatStatusLine(st)
	if strings.Contains(got, "pass") || strings.Contains(got, "fail") || strings.Contains(got, "pending") {
		t.Errorf("formatStatusLine() = %q, should not contain CI status when empty", got)
	}
}

func TestStatusJSONOutput(t *testing.T) {
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

	statuses := []worktreeStatus{
		{
			Path:        "/path/to/main",
			Branch:      "main",
			HEAD:        "abc123",
			Dirty:       false,
			Ahead:       0,
			Behind:      0,
			Current:     false,
			HasUpstream: true,
		},
	}

	err = emitJSONSuccess(statusCmd, map[string]any{"worktrees": statuses})
	if err != nil {
		t.Fatalf("emitJSONSuccess() unexpected error: %v", err)
	}

	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read stdout: %v", err)
	}

	var payload struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
		Data    struct {
			Worktrees []worktreeStatus `json:"worktrees"`
		} `json:"data"`
	}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode json: %v\nraw: %s", err, buf.String())
	}
	if !payload.OK {
		t.Fatal("expected ok=true")
	}
	if payload.Command != "wt status" {
		t.Fatalf("expected command 'wt status', got %q", payload.Command)
	}
	if len(payload.Data.Worktrees) != 1 {
		t.Fatalf("expected 1 worktree, got %d", len(payload.Data.Worktrees))
	}
	wt := payload.Data.Worktrees[0]
	if wt.Branch != "main" {
		t.Errorf("expected branch 'main', got %q", wt.Branch)
	}
	if wt.Dirty {
		t.Error("expected dirty=false")
	}
}

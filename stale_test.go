package main

import (
	"fmt"
	"testing"
	"time"
)

func TestParseCommitDate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantAge bool // true = should return a time in the past
	}{
		{
			name:    "Valid git log date",
			input:   "2024-01-15 10:30:00 +0100",
			wantErr: false,
			wantAge: true,
		},
		{
			name:    "Valid date with different timezone",
			input:   "2024-06-01 08:00:00 -0500",
			wantErr: false,
			wantAge: true,
		},
		{
			name:    "Empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "Whitespace only",
			input:   "   ",
			wantErr: true,
		},
		{
			name:    "Invalid date format",
			input:   "not-a-date",
			wantErr: true,
		},
		{
			name:    "Input with surrounding whitespace",
			input:   "  2024-01-15 10:30:00 +0100  \n",
			wantErr: false,
			wantAge: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCommitDate(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCommitDate(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.wantAge {
				if got.After(time.Now()) {
					t.Errorf("parseCommitDate(%q) returned future time %v", tt.input, got)
				}
			}
		})
	}
}

func TestParseCommitDateRoundTrip(t *testing.T) {
	// Verify parsing returns the correct date values
	input := "2024-03-15 14:30:00 +0000"
	got, err := parseCommitDate(input)
	if err != nil {
		t.Fatalf("parseCommitDate(%q) unexpected error: %v", input, err)
	}

	if got.Year() != 2024 || got.Month() != 3 || got.Day() != 15 {
		t.Errorf("parseCommitDate(%q) = %v, expected 2024-03-15", input, got)
	}
}

func TestIsRemoteBranchDeletedFromOutput(t *testing.T) {
	lsRemoteOutput := `abc123	refs/heads/main
def456	refs/heads/feature/active
ghi789	refs/heads/develop
`

	tests := []struct {
		name   string
		branch string
		want   bool
	}{
		{
			name:   "Branch exists on remote",
			branch: "main",
			want:   false,
		},
		{
			name:   "Feature branch exists on remote",
			branch: "feature/active",
			want:   false,
		},
		{
			name:   "Branch deleted from remote",
			branch: "feature/old-stuff",
			want:   true,
		},
		{
			name:   "Empty branch name",
			branch: "",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRemoteBranchDeletedFromOutput(tt.branch, lsRemoteOutput)
			if got != tt.want {
				t.Errorf("isRemoteBranchDeletedFromOutput(%q) = %v, want %v", tt.branch, got, tt.want)
			}
		})
	}
}

func TestIsRemoteBranchDeletedFromOutputEmpty(t *testing.T) {
	// Empty ls-remote output means no remote branches => all branches are "deleted"
	got := isRemoteBranchDeletedFromOutput("feature/foo", "")
	if !got {
		t.Error("isRemoteBranchDeletedFromOutput with empty output should return true")
	}
}

func TestClassifyStaleWorktree(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name           string
		branch         string
		remoteDeleted  bool
		lastCommitTime time.Time
		staleDays      int
		defaultBase    string
		wantStale      bool
		wantReason     string
	}{
		{
			name:           "Main branch is never stale",
			branch:         "main",
			remoteDeleted:  true,
			lastCommitTime: now.Add(-365 * 24 * time.Hour),
			staleDays:      30,
			defaultBase:    "main",
			wantStale:      false,
			wantReason:     "",
		},
		{
			name:           "Master branch is never stale",
			branch:         "master",
			remoteDeleted:  true,
			lastCommitTime: now.Add(-365 * 24 * time.Hour),
			staleDays:      30,
			defaultBase:    "master",
			wantStale:      false,
			wantReason:     "",
		},
		{
			name:           "Default base branch is never stale even if not main/master",
			branch:         "develop",
			remoteDeleted:  true,
			lastCommitTime: now.Add(-365 * 24 * time.Hour),
			staleDays:      30,
			defaultBase:    "develop",
			wantStale:      false,
			wantReason:     "",
		},
		{
			name:           "Remote deleted branch is stale",
			branch:         "feature/old",
			remoteDeleted:  true,
			lastCommitTime: now,
			staleDays:      30,
			defaultBase:    "main",
			wantStale:      true,
			wantReason:     "remote deleted",
		},
		{
			name:           "Inactive branch is stale",
			branch:         "feature/ancient",
			remoteDeleted:  false,
			lastCommitTime: now.Add(-45 * 24 * time.Hour),
			staleDays:      30,
			defaultBase:    "main",
			wantStale:      true,
			wantReason:     "inactive (45 days)",
		},
		{
			name:           "Recent branch is not stale",
			branch:         "feature/fresh",
			remoteDeleted:  false,
			lastCommitTime: now.Add(-5 * 24 * time.Hour),
			staleDays:      30,
			defaultBase:    "main",
			wantStale:      false,
			wantReason:     "",
		},
		{
			name:           "Branch exactly at threshold is not stale",
			branch:         "feature/borderline",
			remoteDeleted:  false,
			lastCommitTime: now.Add(-30 * 24 * time.Hour),
			staleDays:      30,
			defaultBase:    "main",
			wantStale:      false,
			wantReason:     "",
		},
		{
			name:           "Branch one day over threshold is stale",
			branch:         "feature/just-over",
			remoteDeleted:  false,
			lastCommitTime: now.Add(-31 * 24 * time.Hour),
			staleDays:      30,
			defaultBase:    "main",
			wantStale:      true,
			wantReason:     "inactive (31 days)",
		},
		{
			name:           "Remote deleted takes priority over inactive",
			branch:         "feature/both",
			remoteDeleted:  true,
			lastCommitTime: now.Add(-60 * 24 * time.Hour),
			staleDays:      30,
			defaultBase:    "main",
			wantStale:      true,
			wantReason:     "remote deleted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStale, gotReason := classifyStaleWorktree(tt.branch, tt.remoteDeleted, tt.lastCommitTime, tt.staleDays, tt.defaultBase)
			if gotStale != tt.wantStale {
				t.Errorf("classifyStaleWorktree() stale = %v, want %v", gotStale, tt.wantStale)
			}
			if gotReason != tt.wantReason {
				t.Errorf("classifyStaleWorktree() reason = %q, want %q", gotReason, tt.wantReason)
			}
		})
	}
}

func TestFormatInactiveReason(t *testing.T) {
	tests := []struct {
		days int
		want string
	}{
		{30, "inactive (30 days)"},
		{1, "inactive (1 days)"},
		{365, "inactive (365 days)"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d days", tt.days), func(t *testing.T) {
			got := formatInactiveReason(tt.days)
			if got != tt.want {
				t.Errorf("formatInactiveReason(%d) = %q, want %q", tt.days, got, tt.want)
			}
		})
	}
}

func TestCleanupStaleFlags(t *testing.T) {
	cmd := cleanupCmd

	staleFlag := cmd.Flags().Lookup("stale")
	if staleFlag == nil {
		t.Error("cleanup command missing --stale flag")
	}

	staleDaysFlag := cmd.Flags().Lookup("stale-days")
	if staleDaysFlag == nil {
		t.Error("cleanup command missing --stale-days flag")
	}

	if staleDaysFlag != nil && staleDaysFlag.DefValue != "30" {
		t.Errorf("cleanup --stale-days default = %q, want %q", staleDaysFlag.DefValue, "30")
	}
}

package main

import (
	"testing"
)

func TestGetPRNumber(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "Valid PR number",
			input:   "123",
			want:    "123",
			wantErr: false,
		},
		{
			name:    "Valid GitHub PR URL",
			input:   "https://github.com/owner/repo/pull/456",
			want:    "456",
			wantErr: false,
		},
		{
			name:    "Valid GitLab MR URL",
			input:   "https://gitlab.com/owner/repo/-/merge_requests/789",
			want:    "789",
			wantErr: false,
		},
		{
			name:    "Invalid input",
			input:   "not-a-number",
			want:    "",
			wantErr: true,
		},
		{
			name:    "Invalid URL",
			input:   "https://example.com/pull/123",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getPRNumber(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("getPRNumber() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getPRNumber() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetDefaultBase(t *testing.T) {
	// This is a simple smoke test - actual behavior depends on git state
	result := getDefaultBase()
	if result == "" {
		t.Error("getDefaultBase() returned empty string")
	}
}

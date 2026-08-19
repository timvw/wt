package tmpl

import (
	"testing"
)

func TestRender(t *testing.T) {
	t.Parallel()

	ctx := map[string]any{
		"branch":       "feat-login",
		"worktreeRoot": "/home/user/worktrees",
		"repo":         map[string]string{"Name": "wt", "Owner": "timvw"},
		"env":          map[string]string{"WT_CATEGORY": "work", "EMPTY_VAR": ""},
	}

	tests := []struct {
		name    string
		pattern string
		sep     string
		want    string
		wantErr bool
	}{
		{
			name:    "empty pattern",
			pattern: "",
			sep:     "/",
			want:    "",
		},
		{
			name:    "plain variables",
			pattern: "{.worktreeRoot}/{.repo.Name}/{.branch}",
			sep:     "/",
			want:    "/home/user/worktrees/wt/feat-login",
		},
		{
			name:    "env var set",
			pattern: "{.worktreeRoot}/{.env.WT_CATEGORY}/{.branch}",
			sep:     "/",
			want:    "/home/user/worktrees/work/feat-login",
		},
		{
			name:    "env var unset without default errors",
			pattern: "{.worktreeRoot}/{.env.UNSET_VAR}/{.branch}",
			sep:     "/",
			wantErr: true,
		},
		{
			name:    "misspelled non-env key errors",
			pattern: "{.brnach}",
			sep:     "/",
			wantErr: true,
		},
		// Default-value syntax: {.env.X:-fallback}
		{
			name:    "default used when var unset",
			pattern: "{.worktreeRoot}/{.env.UNSET_VAR:-personal}/{.branch}",
			sep:     "/",
			want:    "/home/user/worktrees/personal/feat-login",
		},
		{
			name:    "default ignored when var set",
			pattern: "{.worktreeRoot}/{.env.WT_CATEGORY:-personal}/{.branch}",
			sep:     "/",
			want:    "/home/user/worktrees/work/feat-login",
		},
		{
			name:    "empty default when var unset",
			pattern: "{.worktreeRoot}/{.env.UNSET_VAR:-}/{.branch}",
			sep:     "/",
			want:    "/home/user/worktrees//feat-login",
		},
		{
			name:    "default containing slash with default separator",
			pattern: "{.worktreeRoot}/{.env.UNSET_VAR:-a/b}/{.branch}",
			sep:     "/",
			want:    "/home/user/worktrees/a/b/feat-login",
		},
		{
			name:    "default containing slash with dash separator",
			pattern: "{.env.UNSET_VAR:-a/b}",
			sep:     "-",
			want:    "a-b",
		},
		{
			name:    "default with set empty var uses empty value",
			pattern: "{.worktreeRoot}/{.env.EMPTY_VAR:-fallback}/{.branch}",
			sep:     "/",
			want:    "/home/user/worktrees//feat-login",
		},
		{
			name:    "multiple defaults in one pattern",
			pattern: "{.env.UNSET_A:-alpha}/{.env.UNSET_B:-beta}",
			sep:     "/",
			want:    "alpha/beta",
		},
		{
			name:    "default mixed with plain env ref",
			pattern: "{.env.WT_CATEGORY}/{.env.MISSING:-fallback}",
			sep:     "/",
			want:    "work/fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Render(tt.pattern, ctx, tt.sep)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Render(%q) succeeded with %q, want error", tt.pattern, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render(%q) error: %v", tt.pattern, err)
			}
			if got != tt.want {
				t.Errorf("Render(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestExpandDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{
			name:    "no defaults unchanged",
			pattern: "{.worktreeRoot}/{.env.FOO}/{.branch}",
			want:    "{.worktreeRoot}/{.env.FOO}/{.branch}",
		},
		{
			name:    "simple default",
			pattern: "{.env.X:-hello}",
			want:    `{envOr "X" "hello" .env}`,
		},
		{
			name:    "empty default",
			pattern: "{.env.X:-}",
			want:    `{envOr "X" "" .env}`,
		},
		{
			name:    "default with slash",
			pattern: "{.env.X:-foo/bar}",
			want:    `{envOr "X" "foo/bar" .env}`,
		},
		{
			name:    "default with double quote escaped",
			pattern: `{.env.X:-he said "hi"}`,
			want:    `{envOr "X" "he said \"hi\"" .env}`,
		},
		{
			name:    "multiple defaults",
			pattern: "{.env.A:-aa}/{.env.B:-bb}",
			want:    `{envOr "A" "aa" .env}/{envOr "B" "bb" .env}`,
		},
		{
			name:    "default mixed with plain",
			pattern: "{.env.SET}/{.env.X:-def}",
			want:    `{.env.SET}/{envOr "X" "def" .env}`,
		},
		{
			name:    "non-env colon-dash left alone",
			pattern: "{.branch:-main}",
			want:    "{.branch:-main}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := expandDefaults(tt.pattern)
			if got != tt.want {
				t.Errorf("expandDefaults(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}

func TestTransform(t *testing.T) {
	t.Parallel()
	tests := []struct {
		sep, in, want string
	}{
		{"/", "feat/foo", "feat/foo"},
		{"-", "feat/foo", "feat-foo"},
		{"_", "feat\\bar", "feat_bar"},
		{"", "a/b\\c", "abc"},
	}
	for _, tt := range tests {
		got := Transform(tt.sep, tt.in)
		if got != tt.want {
			t.Errorf("Transform(%q, %q) = %q, want %q", tt.sep, tt.in, got, tt.want)
		}
	}
}

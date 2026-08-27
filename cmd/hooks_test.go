package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Regression tests for #118: on Windows wt ran every hook through `cmd /c`,
// so a Git Bash user's POSIX hook — which is what all of our documented
// examples are — silently did nothing (post-hooks) or aborted the operation
// with a misleading error (pre-hooks).

func TestHookShell(t *testing.T) {
	shFound := func(string) (string, error) { return `C:\Program Files\Git\usr\bin\sh.exe`, nil }
	shMissing := func(string) (string, error) { return "", errors.New("not found") }

	tests := []struct {
		name       string
		goos       string
		envMsystem string
		envShell   string
		lookPath   func(string) (string, error)
		wantName   string
		wantFlag   string
		wantPOSIX  bool
	}{
		{
			name:      "unix always uses sh",
			goos:      "linux",
			lookPath:  shMissing, // never consulted off Windows
			wantName:  "sh",
			wantFlag:  "-c",
			wantPOSIX: true,
		},
		{
			name:      "macos always uses sh",
			goos:      "darwin",
			lookPath:  shFound,
			wantName:  "sh",
			wantFlag:  "-c",
			wantPOSIX: true,
		},
		{
			name:      "windows with no POSIX shell env keeps cmd",
			goos:      "windows",
			lookPath:  shFound,
			wantName:  "cmd",
			wantFlag:  "/c",
			wantPOSIX: false,
		},
		{
			// The bug: a Git Bash user got cmd, which expands neither $WT_PATH
			// nor provides test/cp/&&.
			name:       "windows under git bash uses sh",
			goos:       "windows",
			envMsystem: "MINGW64",
			envShell:   "/usr/bin/bash",
			lookPath:   shFound,
			wantName:   `C:\Program Files\Git\usr\bin\sh.exe`,
			wantFlag:   "-c",
			wantPOSIX:  true,
		},
		{
			name:      "windows under cygwin uses sh via SHELL alone",
			goos:      "windows",
			envShell:  "/bin/bash",
			lookPath:  shFound,
			wantName:  `C:\Program Files\Git\usr\bin\sh.exe`,
			wantFlag:  "-c",
			wantPOSIX: true,
		},
		{
			// Claiming a POSIX env we cannot actually run is worse than cmd:
			// exec would fail on every hook rather than on the ones using
			// POSIX syntax.
			name:       "windows in POSIX env without sh on PATH falls back to cmd",
			goos:       "windows",
			envMsystem: "MINGW64",
			envShell:   "/usr/bin/bash",
			lookPath:   shMissing,
			wantName:   "cmd",
			wantFlag:   "/c",
			wantPOSIX:  false,
		},
		{
			// Conservative by design: PowerShell users who wrote %WT_PATH%
			// hooks against the old behaviour are unaffected.
			name:      "windows under powershell keeps cmd",
			goos:      "windows",
			envShell:  `C:\Program Files\PowerShell\7\pwsh.exe`,
			lookPath:  shFound,
			wantName:  "cmd",
			wantFlag:  "/c",
			wantPOSIX: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set unconditionally (including to "") so a real MSYSTEM/SHELL on
			// the host runner cannot leak in.
			t.Setenv("MSYSTEM", tt.envMsystem)
			t.Setenv("SHELL", tt.envShell)

			name, flag, posix := hookShell(tt.goos, tt.lookPath)
			if name != tt.wantName || flag != tt.wantFlag || posix != tt.wantPOSIX {
				t.Errorf("hookShell(%q) with MSYSTEM=%q SHELL=%q = (%q, %q, %v), want (%q, %q, %v)",
					tt.goos, tt.envMsystem, tt.envShell,
					name, flag, posix, tt.wantName, tt.wantFlag, tt.wantPOSIX)
			}
		})
	}
}

func TestToPOSIXPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"native windows path", `C:\Users\tim\worktrees\repo\branch`, "C:/Users/tim/worktrees/repo/branch"},
		{"drive root", `C:\`, "C:/"},
		{"unc path", `\\server\share\repo`, "//server/share/repo"},
		{"already posix", "/home/tim/repo", "/home/tim/repo"},
		{"mixed separators", `C:\Users\tim/repo`, "C:/Users/tim/repo"},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toPOSIXPath(tt.in); got != tt.want {
				t.Errorf("toPOSIXPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestAdaptHookEnv(t *testing.T) {
	native := map[string]string{
		"WT_PATH":       `C:\Users\tim\worktrees\repo\feat\x`,
		"WT_MAIN":       `C:\Users\tim\dev\repo`,
		"WT_BRANCH":     `feat\x`,
		"WT_REPO_NAME":  "repo",
		"WT_REPO_HOST":  "github.com",
		"WT_REPO_OWNER": "timvw",
	}

	t.Run("windows with posix shell converts path vars only", func(t *testing.T) {
		got := adaptHookEnv(native, "windows", true)

		want := map[string]string{
			"WT_PATH": "C:/Users/tim/worktrees/repo/feat/x",
			"WT_MAIN": "C:/Users/tim/dev/repo",
			// A branch may legitimately contain a backslash; it is not a path
			// and must survive untouched.
			"WT_BRANCH":     `feat\x`,
			"WT_REPO_NAME":  "repo",
			"WT_REPO_HOST":  "github.com",
			"WT_REPO_OWNER": "timvw",
		}
		for k, w := range want {
			if got[k] != w {
				t.Errorf("adaptHookEnv()[%q] = %q, want %q", k, got[k], w)
			}
		}
		if len(got) != len(want) {
			t.Errorf("adaptHookEnv() returned %d vars, want %d", len(got), len(want))
		}
	})

	t.Run("windows with cmd leaves native paths alone", func(t *testing.T) {
		got := adaptHookEnv(native, "windows", false)
		if got["WT_PATH"] != native["WT_PATH"] {
			t.Errorf("adaptHookEnv()[WT_PATH] = %q, want %q unchanged", got["WT_PATH"], native["WT_PATH"])
		}
	})

	t.Run("unix is unchanged", func(t *testing.T) {
		env := map[string]string{"WT_PATH": "/home/tim/worktrees/repo/feat/x"}
		got := adaptHookEnv(env, "linux", true)
		if got["WT_PATH"] != env["WT_PATH"] {
			t.Errorf("adaptHookEnv()[WT_PATH] = %q, want %q unchanged", got["WT_PATH"], env["WT_PATH"])
		}
	})

	t.Run("does not mutate the caller's map", func(t *testing.T) {
		env := map[string]string{"WT_PATH": `C:\a\b`}
		adaptHookEnv(env, "windows", true)
		if env["WT_PATH"] != `C:\a\b` {
			t.Errorf("adaptHookEnv mutated its argument: WT_PATH = %q", env["WT_PATH"])
		}
	})
}

// TestCmdUnsafeHookVar: cmd.exe expands %WT_PATH% while it parses, so a value
// carrying "&" splits the approved command in two and the tail runs as its own.
// The value is repository-controlled at one remove — a .wt.toml sets the
// worktree pattern, and `wt pr` takes the branch name from the pull request —
// while the approval covers only the commands, so nothing about this asks again.
func TestCmdUnsafeHookVar(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		posix     bool
		wantVar   string
		wantValue string
	}{
		{
			name:      "ampersand in a repo-chosen path",
			env:       map[string]string{"WT_PATH": `C:\wt\repo\feat&calc`, "WT_BRANCH": "feat"},
			wantVar:   "WT_PATH",
			wantValue: `C:\wt\repo\feat&calc`,
		},
		{
			name:      "pipe in a branch name from a pull request",
			env:       map[string]string{"WT_BRANCH": "fix|whoami"},
			wantVar:   "WT_BRANCH",
			wantValue: "fix|whoami",
		},
		{
			name:      "output redirection",
			env:       map[string]string{"WT_REPO_NAME": `a>b`},
			wantVar:   "WT_REPO_NAME",
			wantValue: `a>b`,
		},
		{
			name:      "input redirection",
			env:       map[string]string{"WT_REPO_NAME": `a<b`},
			wantVar:   "WT_REPO_NAME",
			wantValue: `a<b`,
		},
		{
			// cmd's own escape character: it would consume whatever follows.
			name:      "caret",
			env:       map[string]string{"WT_REPO_NAME": `a^b`},
			wantVar:   "WT_REPO_NAME",
			wantValue: `a^b`,
		},
		{
			// An odd number of quotes leaves the rest of the line inside one.
			name:      "quote",
			env:       map[string]string{"WT_REPO_NAME": `a"b`},
			wantVar:   "WT_REPO_NAME",
			wantValue: `a"b`,
		},
		{
			// The end of a line ends a command; what follows is the next one.
			name:      "a line break",
			env:       map[string]string{"WT_REPO_NAME": "a\r\nwhoami"},
			wantVar:   "WT_REPO_NAME",
			wantValue: "a\r\nwhoami",
		},
		{
			name: "an ordinary Windows path is fine",
			env:  map[string]string{"WT_PATH": `C:\Users\tim\wt\repo\feat\x`, "WT_BRANCH": `feat\x`},
		},
		{
			// cmd does not re-expand what it just substituted, so a percent sign
			// in a value is only a percent sign.
			name: "percent is not syntax",
			env:  map[string]string{"WT_BRANCH": "release-50%"},
		},
		{
			name:  "POSIX shells substitute after parsing, so nothing to check",
			env:   map[string]string{"WT_PATH": "/wt/repo/feat&calc"},
			posix: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotVar, gotValue := cmdUnsafeHookVar(tc.env, tc.posix)
			if gotVar != tc.wantVar || gotValue != tc.wantValue {
				t.Errorf("cmdUnsafeHookVar() = (%q, %q), want (%q, %q)",
					gotVar, gotValue, tc.wantVar, tc.wantValue)
			}
		})
	}

	t.Run("reports the same variable every time", func(t *testing.T) {
		// Map order is randomised, and a message that names WT_PATH on one run
		// and WT_MAIN on the next reads like two different problems.
		env := map[string]string{"WT_PATH": `C:\a&b`, "WT_MAIN": `C:\c&d`, "WT_BRANCH": "e&f"}
		first, _ := cmdUnsafeHookVar(env, false)
		for range 20 {
			if got, _ := cmdUnsafeHookVar(env, false); got != first {
				t.Fatalf("cmdUnsafeHookVar() reported %q then %q for the same env", first, got)
			}
		}
	})
}

// TestRunHooksUsesPOSIXShell exercises the whole path end to end: a hook body
// written in POSIX syntax, with the worktree path arriving via $WT_PATH, must
// actually run. On Unix this is the status quo; the value is that it pins the
// contract runHooks has to keep on Windows too.
func TestRunHooksUsesPOSIXShell(t *testing.T) {
	withPreApprovedHooks(t)
	if _, _, posix := hookShell(runtime.GOOS, exec.LookPath); !posix {
		t.Skip("no POSIX shell selected in this environment")
	}
	t.Setenv("WT_HOOKS_DISABLED", "")

	dir := t.TempDir()
	env := buildHookEnv(repoInfo{Main: dir, Name: "repo"}, "feat/x", dir)

	if err := runHooks("post_create", []string{`test -d "$WT_PATH" && touch "$WT_PATH/.hook-ran"`}, env); err != nil {
		t.Fatalf("runHooks() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".hook-ran")); err != nil {
		t.Errorf("hook did not run: %v", err)
	}
}

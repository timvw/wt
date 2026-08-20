package ignore

import (
	"strings"
	"testing"
)

// TestMatch walks the rules in gitignore(5) one at a time: anchoring, "**",
// negation, directory-only patterns, character classes, escapes, and the rule
// that a slash-free pattern matches at any depth.
func TestMatch(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		isDir    bool
		want     bool
	}{
		// --- slash-free patterns match a basename at any depth ---
		{"basename at root", []string{".env"}, ".env", false, true},
		{"basename nested", []string{".env"}, "a/b/.env", false, true},
		{"basename no partial", []string{".env"}, ".environment", false, false},
		{"basename suffix glob", []string{"*.log"}, "var/log/app.log", false, true},
		{"basename glob no match", []string{"*.log"}, "var/log/app.txt", false, false},
		{"basename glob does not cross slash", []string{"*.log"}, "app.log/inner", false, false},
		{"plain name matches directory", []string{"node_modules"}, "node_modules", true, true},
		{"plain name matches nested directory", []string{"node_modules"}, "pkg/node_modules", true, true},

		// --- anchoring ---
		{"leading slash anchors", []string{"/.env"}, ".env", false, true},
		{"leading slash rejects nested", []string{"/.env"}, "a/.env", false, false},
		{"embedded slash anchors", []string{"a/b"}, "a/b", false, true},
		{"embedded slash rejects prefix", []string{"a/b"}, "x/a/b", false, false},
		{"embedded slash rejects suffix", []string{"a/b"}, "a/b/c", false, false},
		{"anchored glob", []string{"docs/*.md"}, "docs/readme.md", false, true},
		{"anchored glob one level only", []string{"docs/*.md"}, "docs/api/readme.md", false, false},

		// --- "**" ---
		{"leading doublestar any depth", []string{"**/foo"}, "foo", false, true},
		{"leading doublestar nested", []string{"**/foo"}, "a/b/foo", false, true},
		{"doublestar prefixed dir", []string{"**/logs/debug.log"}, "a/logs/debug.log", false, true},
		{"trailing doublestar", []string{"abc/**"}, "abc/x/y.txt", false, true},
		{"trailing doublestar excludes self", []string{"abc/**"}, "abc", true, false},
		{"middle doublestar zero segments", []string{"a/**/b"}, "a/b", false, true},
		{"middle doublestar one segment", []string{"a/**/b"}, "a/x/b", false, true},
		{"middle doublestar many segments", []string{"a/**/b"}, "a/x/y/z/b", false, true},
		{"middle doublestar wrong prefix", []string{"a/**/b"}, "q/x/b", false, false},
		{"adjacent stars are not doublestar", []string{"a**b"}, "axx/yyb", false, false},
		{"adjacent stars behave as star", []string{"a**b"}, "axxb", false, true},

		// --- single star / question mark do not cross "/" ---
		{"star stops at slash", []string{"/a*"}, "a/b", false, false},
		{"question mark one char", []string{"file?.txt"}, "file1.txt", false, true},
		{"question mark not slash", []string{"a?b"}, "a/b", false, false},
		{"question mark needs a char", []string{"file?.txt"}, "file.txt", false, false},

		// --- character classes ---
		{"class range", []string{"file[0-9].txt"}, "file7.txt", false, true},
		{"class range miss", []string{"file[0-9].txt"}, "filex.txt", false, false},
		{"class set", []string{"*.[oa]"}, "main.o", false, true},
		{"class negation", []string{"file[!0-9].txt"}, "filex.txt", false, true},
		{"class negation excludes", []string{"file[!0-9].txt"}, "file7.txt", false, false},
		{"class caret negation", []string{"file[^0-9].txt"}, "filex.txt", false, true},
		{"posix class", []string{"[[:digit:]]*.log"}, "1app.log", false, true},
		{"posix class miss", []string{"[[:digit:]]*.log"}, "app.log", false, false},
		{"class never matches slash", []string{"a[!x]b"}, "a/b", false, false},

		// --- directory-only patterns ---
		{"dir only matches dir", []string{"build/"}, "build", true, true},
		{"dir only rejects file", []string{"build/"}, "build", false, false},
		{"dir only nested", []string{"build/"}, "a/build", true, true},
		{"anchored dir only", []string{"/build/"}, "build", true, true},
		{"anchored dir only rejects nested", []string{"/build/"}, "a/build", true, false},

		// --- negation: last matching pattern wins ---
		{"negation re-includes", []string{"*.env", "!keep.env"}, "keep.env", false, false},
		{"negation leaves others", []string{"*.env", "!keep.env"}, "other.env", false, true},
		{"negation order matters", []string{"!keep.env", "*.env"}, "keep.env", false, true},
		{"negation only never matches", []string{"!keep.env"}, "keep.env", false, false},

		// --- escapes ---
		{"escaped hash is literal", []string{`\#notacomment`}, "#notacomment", false, true},
		{"escaped bang is literal", []string{`\!important`}, "!important", false, true},
		{"escaped star is literal", []string{`a\*b`}, "a*b", false, true},
		{"escaped star not a glob", []string{`a\*b`}, "axb", false, false},

		// --- comments, blanks, whitespace ---
		{"comment matches nothing", []string{"# .env"}, ".env", false, false},
		{"blank matches nothing", []string{"   "}, ".env", false, false},
		{"trailing spaces trimmed", []string{".env   "}, ".env", false, true},
		{"escaped trailing space kept", []string{`file\ `}, "file ", false, true},

		// --- multiple patterns ---
		{"union of patterns first", []string{".env", "*.pem"}, ".env", false, true},
		{"union of patterns second", []string{".env", "*.pem"}, "certs/key.pem", false, true},
		{"union of patterns neither", []string{".env", "*.pem"}, "main.go", false, false},

		// --- empty input ---
		{"no patterns", nil, ".env", false, false},
		{"empty path", []string{"*"}, "", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(tt.patterns)
			if err != nil {
				t.Fatalf("New(%q) returned error: %v", tt.patterns, err)
			}
			if got := m.Match(tt.path, tt.isDir); got != tt.want {
				t.Errorf("Match(%q, isDir=%v) with patterns %q = %v, want %v",
					tt.path, tt.isDir, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestDecide(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		path     string
		isDir    bool
		want     Decision
	}{
		{"nothing matches", []string{"*.env"}, "src/main.go", false, Unmatched},
		{"leaf matches", []string{"*.env"}, "app.env", false, Selected},

		// A directory-only pattern covers everything below it, which Match
		// alone cannot see because the leaf is not a directory.
		{"dir-only covers descendant", []string{"secrets/"}, "secrets/key.pem", false, Selected},
		{"dir-only covers deep descendant", []string{"secrets/"}, "secrets/a/b/key.pem", false, Selected},
		{"dir-only does not cover a sibling", []string{"secrets/"}, "public/key.pem", false, Unmatched},
		{"anchored dir covers descendant", []string{"/cache/"}, "cache/a.txt", false, Selected},

		// A negation below a matched directory is honoured, which is where wt
		// deliberately diverges from git's "cannot re-include" rule.
		{"negation below matched dir", []string{"cache/", "!cache/private.key"}, "cache/private.key", false, Rejected},
		{"sibling below matched dir still selected", []string{"cache/", "!cache/private.key"}, "cache/a.txt", false, Selected},
		{"negation on the leaf alone", []string{"!private.key"}, "private.key", false, Rejected},
		{"negated dir rejects descendants", []string{"!cache/"}, "cache/a.txt", false, Rejected},

		// Order still decides between patterns at the same level.
		{"later pattern wins", []string{"!*.env", "*.env"}, "app.env", false, Selected},
		{"more specific ancestor wins over general", []string{"!logs/", "logs/keep.txt"}, "logs/keep.txt", false, Selected},

		{"empty path", []string{"*"}, "", false, Unmatched},
		{"nil matcher", nil, "a", false, Unmatched},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(tt.patterns)
			if err != nil {
				t.Fatalf("New(%q) returned error: %v", tt.patterns, err)
			}
			if got := m.Decide(tt.path, tt.isDir); got != tt.want {
				t.Errorf("Decide(%q, isDir=%v) with patterns %q = %v, want %v",
					tt.path, tt.isDir, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestLiteralPath(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"node_modules", "node_modules"},
		{"node_modules  ", "node_modules"},
		{`logs\ `, "logs "},
		{`a\#b`, "a#b"},
		{`a\\b`, `a\b`},
		{"", ""},
	}

	for _, tt := range tests {
		if got := LiteralPath(tt.raw); got != tt.want {
			t.Errorf("LiteralPath(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestMayContain(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		dir      string
		want     bool
	}{
		{"anchored pattern descends", []string{".claude/settings.local.json"}, ".claude", true},
		{"anchored pattern wrong dir", []string{".claude/settings.local.json"}, ".vscode", false},
		{"unanchored pattern always descends", []string{".env"}, "anything/at/all", true},
		{"doublestar descends", []string{"a/**/b"}, "a/x/y", true},
		{"deep anchored pattern", []string{"a/b/c/d"}, "a/b", true},
		{"exhausted pattern", []string{"a/b"}, "a/b", false},
		{"negation alone does not descend", []string{"!a/b"}, "a", false},
		{"no patterns", nil, "a", false},
		{"glob segment descends", []string{"docs/*/index.md"}, "docs/api", true},
		{"glob segment mismatch", []string{"docs/*/index.md"}, "src", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(tt.patterns)
			if err != nil {
				t.Fatalf("New(%q) returned error: %v", tt.patterns, err)
			}
			if got := m.MayContain(tt.dir); got != tt.want {
				t.Errorf("MayContain(%q) with patterns %q = %v, want %v", tt.dir, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestParseFile(t *testing.T) {
	input := strings.Join([]string{
		"# a comment",
		"",
		".env",
		"   ",
		"node_modules/",
		`\#literal`,
		"!keep.env",
		"# trailing comment",
	}, "\n")

	got, err := ParseFile(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	want := []string{".env", "node_modules/", `\#literal`, "!keep.env"}
	if len(got) != len(want) {
		t.Fatalf("ParseFile returned %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ParseFile[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseFileEmpty(t *testing.T) {
	got, err := ParseFile(strings.NewReader(""))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ParseFile on empty input = %q, want no patterns", got)
	}
}

func TestParseFileStripsBOMAndCRLF(t *testing.T) {
	got, err := ParseFile(strings.NewReader("\ufeff.env\r\nnode_modules/\r\n"))
	if err != nil {
		t.Fatalf("ParseFile returned error: %v", err)
	}
	want := []string{".env", "node_modules/"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ParseFile = %q, want %q", got, want)
	}
}

func TestNewRejectsDegeneratePatterns(t *testing.T) {
	for _, p := range []string{"!", "/", "!/"} {
		if _, err := New([]string{p}); err == nil {
			t.Errorf("New(%q) succeeded, want an error", p)
		}
	}
}

func TestEmptyAndPatterns(t *testing.T) {
	m, err := New([]string{"# comment", "", ".env"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if m.Empty() {
		t.Error("Empty() = true, want false with one real pattern")
	}
	if got := m.Patterns(); len(got) != 1 || got[0] != ".env" {
		t.Errorf("Patterns() = %q, want [.env]", got)
	}

	blank, err := New([]string{"# only a comment"})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if !blank.Empty() {
		t.Error("Empty() = false, want true when every line is a comment")
	}

	var nilMatcher *Matcher
	if !nilMatcher.Empty() {
		t.Error("nil Matcher should report Empty")
	}
	if nilMatcher.Match("x", false) {
		t.Error("nil Matcher should never match")
	}
	if nilMatcher.MayContain("x") {
		t.Error("nil Matcher should never report MayContain")
	}
}

// Package ignore matches paths against gitignore-syntax patterns.
//
// The semantics follow gitignore(5) so that a pattern written for a
// .gitignore, a .worktreeinclude, or a wt [files] list behaves the same way
// everywhere. It is a self-contained implementation rather than a dependency,
// matching how internal/fuzzy and internal/tmpl are handled.
//
// The matcher answers "does this path match", not "is this path ignored by
// git": wt asks git itself which paths are ignored (git ls-files) and uses
// this package only to narrow that answer down to what the user asked for.
package ignore

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Matcher matches paths against a set of gitignore-syntax patterns.
type Matcher struct {
	patterns []pattern
}

// pattern is one compiled gitignore line.
type pattern struct {
	// raw is the line as written, kept for error messages.
	raw string
	// glob is the pattern body: negation marker, trailing slash and any
	// leading slash removed.
	glob string
	// negate marks a "!" pattern, which re-includes a path an earlier
	// pattern excluded.
	negate bool
	// dirOnly marks a pattern written with a trailing "/", which matches
	// directories only.
	dirOnly bool
	// anchored marks a pattern containing a "/" anywhere (including a leading
	// one). Such a pattern is matched against the whole relative path;
	// an unanchored pattern is matched against the basename at any depth.
	anchored bool
	// segs is glob split on "/", used by MayContain to decide whether a
	// directory is worth descending into.
	segs []string
}

// New compiles patterns. Later patterns take precedence over earlier ones
// (gitignore semantics), so a "!" negation can re-include a previously
// excluded path.
//
// Blank lines and "#" comments are skipped, so a slice read straight from a
// .worktreeinclude and a slice written in TOML compile identically.
func New(patterns []string) (*Matcher, error) {
	m := &Matcher{}
	for _, raw := range patterns {
		p, ok, err := compile(raw)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		m.patterns = append(m.patterns, p)
	}
	return m, nil
}

// Empty reports whether the matcher holds no patterns, and so can never match.
func (m *Matcher) Empty() bool {
	return m == nil || len(m.patterns) == 0
}

// Match reports whether rel (a slash-separated path relative to the root)
// matches. isDir affects trailing-slash patterns.
//
// Patterns are consulted last-to-first and the first one that matches decides,
// which is how a later "!" line re-includes a path an earlier line excluded.
func (m *Matcher) Match(rel string, isDir bool) bool {
	if m == nil {
		return false
	}
	rel = strings.Trim(rel, "/")
	if rel == "" {
		return false
	}
	base := rel
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		base = rel[i+1:]
	}

	for i := len(m.patterns) - 1; i >= 0; i-- {
		p := m.patterns[i]
		if p.dirOnly && !isDir {
			continue
		}
		target := base
		if p.anchored {
			target = rel
		}
		if wildmatch(p.glob, target) {
			return !p.negate
		}
	}
	return false
}

// MayContain reports whether any descendant of the directory rel could match.
//
// It exists so a walk can prune directories that cannot possibly hold a match,
// which is what keeps "copy .claude/settings.local.json" from reading all of a
// fully-ignored node_modules. It is deliberately conservative: a true answer
// only costs a directory read, while a wrong false answer would lose files.
func (m *Matcher) MayContain(rel string) bool {
	if m == nil || len(m.patterns) == 0 {
		return false
	}
	rel = strings.Trim(rel, "/")
	var dirSegs []string
	if rel != "" {
		dirSegs = strings.Split(rel, "/")
	}

	for _, p := range m.patterns {
		if p.negate {
			continue
		}
		if !p.anchored {
			// An unanchored pattern matches a basename at any depth, so
			// anything below rel is a candidate.
			return true
		}
		if segsMayContain(p.segs, dirSegs) {
			return true
		}
	}
	return false
}

// Patterns returns the pattern lines the matcher was compiled from, in order.
func (m *Matcher) Patterns() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.patterns))
	for _, p := range m.patterns {
		out = append(out, p.raw)
	}
	return out
}

// segsMayContain reports whether an anchored pattern's segments could still
// match something below the directory named by dir's segments.
func segsMayContain(pat, dir []string) bool {
	pi := 0
	for di := 0; di < len(dir); di++ {
		if pi >= len(pat) {
			// The pattern ran out before the directory did: the directory is
			// at or below a full match, which the caller handles via Match.
			return false
		}
		if pat[pi] == "**" {
			// "**" absorbs any number of segments, so anything below matches.
			return true
		}
		if !wildmatch(pat[pi], dir[di]) {
			return false
		}
		pi++
	}
	// Segments left over in the pattern are what a descendant would supply.
	return pi < len(pat)
}

// ParseFile reads gitignore-syntax patterns from r, stripping comments and
// blank lines.
func ParseFile(r io.Reader) ([]string, error) {
	var out []string
	scanner := bufio.NewScanner(r)
	// .worktreeinclude lines are paths; the default 64KiB token limit is
	// generous, but a file with no newline at all should fail loudly rather
	// than silently truncate.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// A UTF-8 BOM on the first line would otherwise become part of the
		// first pattern.
		line = strings.TrimPrefix(line, "\ufeff")
		if _, ok, err := compile(line); err != nil || !ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// compile turns one gitignore line into a pattern. ok is false for lines that
// contribute nothing (blank lines and comments).
func compile(raw string) (pattern, bool, error) {
	line := trimTrailingSpaces(raw)
	if line == "" {
		return pattern{}, false, nil
	}
	if strings.HasPrefix(line, "#") {
		return pattern{}, false, nil
	}

	p := pattern{raw: raw}
	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	} else if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
		// An escaped leading "#" or "!" is a literal one.
		line = line[1:]
	}
	if line == "" {
		return pattern{}, false, fmt.Errorf("ignore: pattern %q has no body", raw)
	}

	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
		if line == "" {
			return pattern{}, false, fmt.Errorf("ignore: pattern %q matches nothing", raw)
		}
	}

	// A "/" anywhere — including a leading one — makes the pattern relative to
	// the root rather than a basename match. gitignore(5): "if the pattern
	// does not contain a slash, Git treats it as a shell glob pattern and
	// checks for a match against the pathname's final component".
	p.anchored = strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return pattern{}, false, fmt.Errorf("ignore: pattern %q matches nothing", raw)
	}

	p.glob = line
	p.segs = strings.Split(line, "/")
	return p, true, nil
}

// trimTrailingSpaces drops trailing spaces, which gitignore ignores unless the
// last one is backslash-escaped. Only spaces, matching git: a trailing tab is
// part of the pattern.
func trimTrailingSpaces(s string) string {
	end := len(s)
	for end > 0 && s[end-1] == ' ' {
		// Count the backslashes immediately before this space: an odd number
		// escapes it, so the space is significant and trimming stops.
		backslashes := 0
		for i := end - 2; i >= 0 && s[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 1 {
			break
		}
		end--
	}
	return s[:end]
}

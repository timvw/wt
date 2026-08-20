package ignore

import "strings"

// wildmatch reports whether text matches the glob pattern p using gitignore's
// pathname semantics: "*" and "?" never cross a "/", while a "**" that forms a
// complete path segment does.
//
// It mirrors git's wildmatch() with WM_PATHNAME set, which is the mode
// gitignore matching always uses.
func wildmatch(p, text string) bool {
	return match(p, 0, text, 0)
}

// match compares p[pi:] against text[ti:]. Indices are carried rather than
// slices so a "**" can tell whether it starts a path segment by looking at the
// byte before it.
func match(p string, pi int, text string, ti int) bool {
	for pi < len(p) {
		c := p[pi]
		if ti >= len(text) && c != '*' {
			return false
		}

		switch c {
		case '?':
			if text[ti] == '/' {
				return false
			}
			pi++
			ti++

		case '*':
			start := pi
			for pi < len(p) && p[pi] == '*' {
				pi++
			}
			// "**" only spans "/" when it is a whole path segment: "**/x",
			// "x/**", "x/**/y" or a bare "**". Anywhere else ("a**b") it
			// degrades to a plain "*", as git does.
			matchSlash := false
			if pi-start >= 2 {
				atSegStart := start == 0 || p[start-1] == '/'
				atSegEnd := pi >= len(p) || p[pi] == '/'
				if atSegStart && atSegEnd {
					matchSlash = true
					if pi < len(p) && p[pi] == '/' {
						// "a/**/b" also matches "a/b": let the "**" stand for
						// zero segments by skipping it and its slash.
						if match(p, pi+1, text, ti) {
							return true
						}
					}
				}
			}

			if pi >= len(p) {
				// A trailing "**" matches the rest of the path; a trailing "*"
				// matches only within the current segment.
				if !matchSlash && strings.Contains(text[ti:], "/") {
					return false
				}
				return true
			}

			for k := ti; k <= len(text); k++ {
				if k > ti && !matchSlash && text[k-1] == '/' {
					// The star would have to swallow a "/" to get any further.
					return false
				}
				if match(p, pi, text, k) {
					return true
				}
			}
			return false

		case '[':
			ok, next := matchClass(p, pi, text[ti])
			if next < 0 {
				// Unterminated "[": git treats it as a literal.
				if text[ti] != '[' {
					return false
				}
				pi++
				ti++
				continue
			}
			if !ok {
				return false
			}
			pi = next
			ti++

		case '\\':
			// A backslash escapes the next byte, which is then matched
			// literally. A trailing backslash matches nothing.
			if pi+1 >= len(p) {
				return false
			}
			if text[ti] != p[pi+1] {
				return false
			}
			pi += 2
			ti++

		default:
			if text[ti] != c {
				return false
			}
			pi++
			ti++
		}
	}
	return ti == len(text)
}

// matchClass evaluates the bracket expression starting at p[pi] against ch.
// It returns whether ch is in the class and the pattern index just past the
// closing "]", or -1 if the class is unterminated.
func matchClass(p string, pi int, ch byte) (bool, int) {
	i := pi + 1
	negated := false
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		negated = true
		i++
	}

	matched := false
	first := true
	for i < len(p) {
		if p[i] == ']' && !first {
			// A path separator is never a member of a class in pathname mode,
			// so "[^x]" cannot be used to step out of a directory.
			if ch == '/' {
				return false, i + 1
			}
			return matched != negated, i + 1
		}
		first = false

		// POSIX character class, e.g. "[[:alpha:]]".
		if p[i] == '[' && i+1 < len(p) && p[i+1] == ':' {
			if end := strings.Index(p[i+2:], ":]"); end >= 0 {
				name := p[i+2 : i+2+end]
				if matchPOSIXClass(name, ch) {
					matched = true
				}
				i += 2 + end + 2
				continue
			}
		}

		lo := p[i]
		if lo == '\\' && i+1 < len(p) {
			i++
			lo = p[i]
		}
		i++

		// A range "a-z", but a "-" just before the closing "]" is a literal.
		if i+1 < len(p) && p[i] == '-' && p[i+1] != ']' {
			i++
			hi := p[i]
			if hi == '\\' && i+1 < len(p) {
				i++
				hi = p[i]
			}
			i++
			if ch >= lo && ch <= hi {
				matched = true
			}
			continue
		}
		if ch == lo {
			matched = true
		}
	}
	return false, -1
}

// matchPOSIXClass reports whether ch belongs to the named POSIX class.
// Unknown class names match nothing, which is how git treats them.
func matchPOSIXClass(name string, ch byte) bool {
	switch name {
	case "alpha":
		return isAlpha(ch)
	case "digit":
		return ch >= '0' && ch <= '9'
	case "alnum":
		return isAlpha(ch) || (ch >= '0' && ch <= '9')
	case "upper":
		return ch >= 'A' && ch <= 'Z'
	case "lower":
		return ch >= 'a' && ch <= 'z'
	case "space":
		return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\v' || ch == '\f' || ch == '\r'
	case "blank":
		return ch == ' ' || ch == '\t'
	case "punct":
		return ch > 32 && ch < 127 && !isAlpha(ch) && (ch < '0' || ch > '9')
	case "xdigit":
		return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
	case "print":
		return ch >= 32 && ch < 127
	case "graph":
		return ch > 32 && ch < 127
	case "cntrl":
		return ch < 32 || ch == 127
	default:
		return false
	}
}

func isAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

package tmpl

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
)

// Render executes pattern as a Go template with "{" "}" delimiters against ctx.
// Empty pattern returns "". Missing keys are an error.
//
// sep is the separator used to transform "/" and "\" in value variables; it is
// applied to default values in {.env.X:-fallback} the same way EnvMap applies
// it to actual environment variable values.
//
// Bash-style defaults are supported for environment-variable references:
//
//	{.env.X:-fallback}   → uses "fallback" when X is unset
//	{.env.X:-}           → uses "" when X is unset
//	{.env.X}             → errors when X is unset (unchanged behaviour)
//
// The default value may contain any character except "}".
func Render(pattern string, ctx any, sep string) (string, error) {
	if pattern == "" {
		return "", nil
	}
	processed := expandDefaults(pattern)
	tpl, err := template.New("wt").
		Delims("{", "}").
		Option("missingkey=error").
		Funcs(template.FuncMap{
			"envOr": func(key, fallback string, env map[string]string) string {
				if v, ok := env[key]; ok {
					return v
				}
				return Transform(sep, fallback)
			},
		}).
		Parse(processed)
	if err != nil {
		return "", fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("render %q: %w", pattern, err)
	}
	return buf.String(), nil
}

// expandDefaults rewrites bash-style defaults in env references:
//
//	{.env.VAR:-fallback}  →  {envOr "VAR" "fallback" .env}
//	{.env.VAR:-}          →  {envOr "VAR" "" .env}
//
// Plain {.env.VAR} references (no ":-") are left untouched so that
// missingkey=error still catches misspelled variable names.
func expandDefaults(pattern string) string {
	const prefix = "{.env."
	// Fast path: nothing to rewrite.
	if !strings.Contains(pattern, ":-") {
		return pattern
	}

	var buf strings.Builder
	buf.Grow(len(pattern))
	i := 0
	for i < len(pattern) {
		// Look for the {.env. prefix.
		if i+len(prefix) < len(pattern) && pattern[i:i+len(prefix)] == prefix {
			// Read the variable name ([A-Za-z_][A-Za-z0-9_]*).
			j := i + len(prefix)
			nameStart := j
			for j < len(pattern) && isEnvVarChar(pattern[j]) {
				j++
			}
			name := pattern[nameStart:j]
			// Must have a non-empty name followed by ":-".
			if len(name) > 0 && j+1 < len(pattern) && pattern[j] == ':' && pattern[j+1] == '-' {
				// Consume the default up to the next unescaped '}'.
				k := j + 2
				end := strings.IndexByte(pattern[k:], '}')
				if end >= 0 {
					def := pattern[k : k+end]
					buf.WriteString(`{envOr "`)
					buf.WriteString(name)
					buf.WriteString(`" "`)
					buf.WriteString(escapeGoString(def))
					buf.WriteString(`" .env}`)
					i = k + end + 1
					continue
				}
			}
		}
		buf.WriteByte(pattern[i])
		i++
	}
	return buf.String()
}

func isEnvVarChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_'
}

// escapeGoString escapes double-quotes and backslashes inside a Go
// template string literal so the value can be embedded between "…".
func escapeGoString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// Transform replaces "/" and "\" in s with sep. Apply to user-supplied value
// variables (branch names, owner slugs, env values) before placing them in a
// template context so that separators stay consistent with the configured style.
func Transform(sep, s string) string {
	s = strings.ReplaceAll(s, "/", sep)
	s = strings.ReplaceAll(s, "\\", sep)
	return s
}

// EnvMap builds a map of all current environment variables with values
// transformed via Transform(sep, v). Pass this as {.env} in template contexts.
func EnvMap(sep string) map[string]string {
	m := make(map[string]string, 64)
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			m[k] = Transform(sep, v)
		}
	}
	return m
}

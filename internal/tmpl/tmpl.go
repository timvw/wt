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
func Render(pattern string, ctx any) (string, error) {
	if pattern == "" {
		return "", nil
	}
	tpl, err := template.New("wt").
		Delims("{", "}").
		Option("missingkey=error").
		Parse(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("render %q: %w", pattern, err)
	}
	return buf.String(), nil
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

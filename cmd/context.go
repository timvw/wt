package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/timvw/wt/internal/tmpl"
)

// ContextRule supplies environment variables to pattern rendering when the
// command is operating on a path beneath when_path.
//
// It is wt's answer to "everything under ~/dev/repos/work is work", modelled on
// git's includeIf rather than on directory traversal: the mapping lives in one
// user-owned file, so there is no ancestor .wt.toml to plant in a shared
// directory (the CVE-2022-24765 / safe.directory lesson). See #138.
type ContextRule struct {
	WhenPath string            `toml:"when_path"`
	Env      map[string]string `toml:"env"`
}

// contextRules holds the effective rules in the order they are composed:
// global git config first, then the user's config file.
//
// Read from those two sources only — never from a repo's committed .wt.toml,
// and not from local git config. A cloned repository must not be able to
// redirect where your worktrees land, which is the same boundary that keeps
// root, repo_root and repo_pattern out of .wt.toml.
var contextRules []ContextRule

// gitContextPrefix is the dotted key prefix a context rule lives under:
// `wt.context.<name>.<field>` parses as section wt, subsection context.<name>,
// variable <field>. The subsection is what gives a rule a name, so a later
// `git config --global --remove-section 'wt.context.work'` can target it.
const gitContextPrefix = "wt.context."

// contextRulesFromGitConfig parses wt.context.* entries into rules.
//
//	git config --global wt.context.work.whenpath "~/dev/repos/work"
//	git config --global --add wt.context.work.env "WT_CATEGORY=work"
//	git config --global --add wt.context.work.env "WT_ORG=acme"
//
// Rules keep the order git listed them in, first appearance of a name deciding
// its position, so composition follows the file rather than the map iteration
// order Go would otherwise hand out.
//
// `env` is multi-valued — one NAME=VALUE per entry, added with `--add`. A value
// without "=" or with an empty name is malformed and skipped rather than
// guessed at; there is no reading of a bare "WT_CATEGORY" that is obviously
// right, and silently inventing an empty value would collapse a path segment.
//
// Only ever called with the global scope. A path rule scoped to a single
// repository is redundant — that repo could set wt.pattern directly — and
// keeping rules out of --local preserves the user-owned/repo-owned boundary,
// for the same reason a committed .wt.toml cannot supply them. The system scope
// is not read either, but for a duller reason: wt reads no system git config at
// all today, and adding it for this one setting would mean wt.context.* obeyed
// a scope that wt.root ignored.
func contextRulesFromGitConfig(entries []gitConfigEntry) []ContextRule {
	var (
		rules []ContextRule
		index = map[string]int{}
	)

	for _, entry := range entries {
		// git preserves the case of a subsection, so `context` itself may be
		// spelled with capitals even though `wt` and the variable are not.
		// Match case-insensitively, then slice the original key so the rule's
		// own name keeps the case it was written with.
		lower := strings.ToLower(entry.Key)
		if !strings.HasPrefix(lower, gitContextPrefix) {
			continue
		}
		rest := entry.Key[len(gitContextPrefix):]

		// The name may contain dots of its own (`wt.context.acme.api.env`), so
		// split on the last one: everything before it names the rule.
		dot := strings.LastIndex(rest, ".")
		if dot <= 0 {
			continue
		}
		name, field := rest[:dot], strings.ToLower(rest[dot+1:])

		// Resolve the value before the rule is created, so an unrecognised
		// field or a malformed env entry does not leave an empty rule behind.
		var envKey, envValue string
		switch field {
		case "whenpath":
		case "env":
			key, value, ok := strings.Cut(entry.Value, "=")
			key = strings.TrimSpace(key)
			if !ok || key == "" {
				continue
			}
			envKey, envValue = key, strings.TrimSpace(value)
		default:
			continue
		}

		at, seen := index[name]
		if !seen {
			at = len(rules)
			index[name] = at
			rules = append(rules, ContextRule{})
		}

		if field == "whenpath" {
			rules[at].WhenPath = strings.TrimSpace(entry.Value)
			continue
		}
		if rules[at].Env == nil {
			rules[at].Env = map[string]string{}
		}
		rules[at].Env[envKey] = envValue
	}

	return rules
}

// contextEnv returns the variables the matching rules supply for target.
//
// Every matching rule applies and later definitions win per variable, so a
// broad rule can set a common value and a narrower one override a single key.
// That is git config's own rule for repeated keys, which is worth matching
// given these rules are modelled on includeIf.
//
// An empty target matches nothing: it means the caller had no path to test
// (a clone from a directory that has since been removed, say), and a rule that
// fired on "no path" would apply everywhere.
func contextEnv(target string) map[string]string {
	if len(contextRules) == 0 || target == "" {
		return nil
	}

	resolved := absCanonicalPath(target)
	if resolved == "" {
		return nil
	}

	var out map[string]string
	for _, rule := range contextRules {
		if len(rule.Env) == 0 || rule.WhenPath == "" {
			continue
		}
		if !pathWithin(rule.WhenPath, resolved) {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(rule.Env))
		}
		for k, v := range rule.Env {
			out[k] = v
		}
	}
	return out
}

// pathWithin reports whether target is prefix itself or lives beneath it.
//
// when_path is a path prefix rather than a glob: the whole use case is
// "everything under this directory", and filepath.Match has no "**", so a glob
// would mean a new dependency for no expressiveness anyone asked for.
func pathWithin(prefix, target string) bool {
	root := absCanonicalPath(expandHome(prefix))
	if root == "" {
		return false
	}

	if pathsEqual(root, target) {
		return true
	}

	// Compare on a separator boundary so ~/dev/repos/work does not match
	// ~/dev/repos/workshop.
	if !strings.HasSuffix(root, string(os.PathSeparator)) {
		root += string(os.PathSeparator)
	}
	if len(target) <= len(root) {
		return false
	}
	return pathsEqual(target[:len(root)], root)
}

// absCanonicalPath makes a path absolute before handing it to canonicalPath,
// so that a rule written against ~/dev/repos still matches when $HOME or the
// repo root is reached through a symlink.
//
// The absolute step is what canonicalPath alone does not do, and it matters
// here: a relative when_path or working directory would otherwise compare
// against an absolute target and never match. Symlink resolution stays
// best-effort — EvalSymlinks fails on a path that does not exist yet, which is
// routine for a clone destination.
func absCanonicalPath(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return canonicalPath(abs)
}

// pathsEqual compares two path fragments with the case sensitivity of the
// platform.
//
// Keyed on GOOS rather than probing the filesystem the way
// filesystemCaseInsensitive does: that probe creates a temp file in the nearest
// existing directory, which is far too much work for a prefix test, and it
// needs a directory that exists — which a clone destination does not yet.
// Treating macOS and Windows as case-insensitive matches their default
// configuration, and being wrong on a case-sensitive APFS volume only makes a
// rule match a spelling the user did not write.
func pathsEqual(a, b string) bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// templateEnv builds the {.env} map for pattern rendering: the variables that
// matching context rules supply, with the real environment layered on top.
//
// The order matters. Rules sit *underneath* os.Environ so an exported variable
// always wins — including one that is set but empty, which EnvMap reports as a
// present key. That is what keeps the documented `WT_CATEGORY= wt clone o/r`
// escape hatch collapsing the segment instead of picking up a rule's value.
//
// Rule values go through Transform for the same reason environment values do:
// a separator of "-" should turn a value of "a/b" into "a-b" whichever source
// supplied it.
func templateEnv(sep, matchPath string) map[string]string {
	rules := contextEnv(matchPath)
	env := tmpl.EnvMap(sep)
	if len(rules) == 0 {
		return env
	}

	merged := make(map[string]string, len(env)+len(rules))
	for k, v := range rules {
		merged[k] = tmpl.Transform(sep, v)
	}
	for k, v := range env {
		merged[k] = v
	}
	return merged
}

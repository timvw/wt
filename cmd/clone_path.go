package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/timvw/wt/internal/tmpl"
)

// defaultRepoPattern places a canonical clone under the top-level repo root,
// grouped by forge and owner so repositories from different hosts and accounts
// stay separated without any extra configuration.
//
// The trailing branch segment names the remote's default branch (e.g. "main"),
// which makes the clone directory a valid main-worktree slot: sibling worktree
// strategies then place feature worktrees next to it as <repo>/<branch>.
//
// Callers who want an extra grouping level (a "category", a client, a year)
// add it with an environment variable, e.g.
//
//	repo_pattern = "{.repoRoot}/{.env.WT_CATEGORY}/{.repo.Owner}/{.repo.Name}/{.branch}"
const defaultRepoPattern = "{.repoRoot}/{.repo.Host}/{.repo.Owner}/{.repo.Name}/{.branch}"

// repoPlacementPath returns the absolute path where a repository's canonical
// clone should live, per repo_pattern and the top-level repo_root. info must
// carry Owner and Name (from parseRemoteURL); branch is the remote's default
// branch, used when the pattern references {.branch}.
func repoPlacementPath(info repoInfo, branch string) (string, error) {
	if info.Name == "" || info.Owner == "" {
		return "", fmt.Errorf("cannot derive placement: need owner/name (got owner=%q name=%q); pass an explicit destination", info.Owner, info.Name)
	}

	// Host, Owner, Name and branch all come from the clone source, which may be
	// a URL the user pasted from somewhere. A ".." component in any of them
	// would walk the rendered path out of repo_root — via the path
	// (https://host/../../tmp/pwn.git) or via the host itself, since an
	// scp-like "../escape:owner/repo.git" parses "../escape" as the host. Refuse
	// rather than write outside the configured layout. A ".." inside a segment
	// ("a..b") is a legitimate host and stays allowed.
	//
	// The same four fields must also not carry the characters expandHome acts
	// on, because it runs on the rendered path — after these values are in it.
	// "$HOME" in an owner is not a directory named $HOME, it is the expansion
	// that "https://evil.example/$HOME/.config/wt.git" was written to get: with
	// a relative repo_pattern the clone lands on wt's own config directory, and
	// what git checks out there becomes config.toml and trust.toml. Neither the
	// ".." check above nor the repo_root anchoring below sees it, since the
	// expansion happens after both. Verified: it did.
	for _, v := range []struct{ what, value string }{
		{"host", info.Host},
		{"owner", info.Owner},
		{"repository name", info.Name},
		{"default branch", branch},
	} {
		if hasDotDotSegment(v.value) {
			return "", fmt.Errorf("refusing to derive placement: %s %q contains a %q path segment; pass an explicit destination", v.what, v.value, "..")
		}
		if char := expansionMetachar(v.value); char != "" {
			return "", fmt.Errorf("refusing to derive placement: %s %q contains %q, which expands to somewhere the URL chose rather than to a directory of that name; pass an explicit destination", v.what, v.value, char)
		}
	}

	pattern := repoPattern
	if pattern == "" {
		pattern = defaultRepoPattern
	}

	sep := worktreeSeparator

	// No repository exists yet, so context rules match the working directory —
	// the only signal available, and the one the user is expressing intent with
	// when they choose where to stand before cloning. It is an input to the
	// pattern, never the rendered destination, so nothing is circular.
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}

	ctx := map[string]any{
		"repoRoot": reposRoot,
		"repo":     map[string]any{"Host": info.Host, "Owner": tmpl.Transform(sep, info.Owner), "Name": info.Name},
		"branch":   tmpl.Transform(sep, branch),
		"env":      templateEnv(sep, cwd),
	}
	rendered, err := tmpl.Render(pattern, ctx, sep)
	if err != nil {
		return "", err
	}

	path := expandHome(filepath.FromSlash(rendered))
	// A pattern that omits {.repoRoot} renders a path relative to the caller's
	// current directory, which is never what a clone wants. Anchor it under the
	// repo root, the same way renderWorktreePath anchors relative worktree
	// patterns under the worktree root.
	//
	// Keyed on the pattern rather than on IsAbs(path): a repo_root that is
	// relative, or rooted-but-driveless on Windows ("\data\repos"), renders a
	// path that IsAbs rejects, and anchoring it would prepend the root a second
	// time. If the pattern named {.repoRoot}, it is already anchored.
	if !strings.Contains(pattern, "{.repoRoot}") && !filepath.IsAbs(path) {
		path = filepath.Join(reposRoot, path)
	}
	path = filepath.Clean(path)

	// Defence in depth: the refusals above cover the routes onto wt's own state
	// that are known, this covers the destination whatever route reached it. A
	// clone writes a whole repository at once, so landing here is the same
	// substitution renderWorktreePath refuses — see wtStateAtPath.
	if owned := wtStateAtPath(path); owned != "" {
		return "", fmt.Errorf(
			"this clone would land at %s, which is %s.\n"+
				"The files cloned there would become wt's own config file and approval store,\n"+
				"which is what decides whether any repository's hooks run.\n"+
				"Change the pattern — it currently comes from %s — or pass an explicit destination",
			path, owned, repoPatternSourceLabel())
	}
	return path, nil
}

// expansionMetachar returns the character in s that expandHome would act on, or
// "" when there is none. "$" and "%" are refused on every platform, not only
// where they expand: the same URL should be placed the same way everywhere, and
// a repository whose name genuinely contains one is served by the explicit
// destination the refusal points at. A "~" only expands leading, so only a
// leading one is refused — "a/~/b" is a directory named "~".
func expansionMetachar(s string) string {
	switch {
	case strings.Contains(s, "$"):
		return "$"
	case strings.Contains(s, "%"):
		return "%"
	case strings.HasPrefix(s, "~"):
		return "~"
	}
	return ""
}

// repoPatternSourceLabel names the layer repo_pattern came from, so the refusal
// above says whose setting to go and change. repo_pattern is never read from a
// repository's .wt.toml, so this always names something of the user's; what the
// clone source supplies is the value interpolated into it.
func repoPatternSourceLabel() string {
	if configSources.RepoPattern != "" {
		return configSources.RepoPattern
	}
	return "the built-in default"
}

// hasDotDotSegment reports whether s contains a ".." path segment under either
// separator. Sub-segments such as "gitlab-group/..hidden" are fine; only a
// component that is exactly ".." can climb out of the rendered path.
func hasDotDotSegment(s string) bool {
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

// resolveDefaultBranch queries the remote's default branch via git ls-remote,
// which is authoritative for every source (GitHub, GitLab, local, file://).
// Falls back to "main" if the remote is unreachable or HEAD is unresolvable.
func resolveDefaultBranch(cloneURL string) string {
	if cloneURL == "" {
		return "main"
	}
	// "--" keeps a source that begins with "-" from being read as a git option.
	c := exec.Command("git", "ls-remote", "--symref", "--", cloneURL, "HEAD")
	out, err := c.Output()
	if err != nil {
		return "main"
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasSuffix(line, "\tHEAD") && strings.HasPrefix(line, "ref: refs/heads/") {
			return strings.TrimPrefix(strings.TrimSuffix(line, "\tHEAD"), "ref: refs/heads/")
		}
	}
	return "main"
}

package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/timvw/wt/internal/tmpl"
)

// Category is an organizational context for repositories: where their canonical
// clones live (RepoRoot) plus the auth profile used to reach them. Categories
// are the base tier that `wt clone` places repositories into; higher-level
// features (workspaces) reuse the same concept.
type Category struct {
	RepoRoot    string `toml:"repo_root"`    // canonical clone root, e.g. ~/dev/repos/work
	GHAuth      string `toml:"gh_auth"`      // gh account (gh auth switch --user)
	GitProtocol string `toml:"git_protocol"` // ssh|https for owner/repo -> clone URL
	GLabHost    string `toml:"glab_host"`    // glab --hostname / GITLAB_HOST
}

// defaultRepoPattern places a canonical clone under the category's repo root.
// The branch segment names the default branch (e.g. "main"), making the clone
// a valid main-worktree slot for sibling worktree strategies.
// Override with repo_pattern; add {.repo.Host} for multi-forge categories.
const defaultRepoPattern = "{.category.RepoRoot}/{.repo.Owner}/{.repo.Name}/{.branch}"

// builtinCategories are the out-of-the-box categories shipped with wt.
// They intentionally set no repo_root — it defaults to reposRoot/<name>
// (see resolveCategory), so all repos relocate by changing the single
// top-level repo_root.
func builtinCategories() map[string]Category {
	return map[string]Category{
		"work":     {GHAuth: "work"},
		"personal": {GHAuth: "personal"},
		"oss":      {GHAuth: "oss"},
	}
}

// resolveCategory returns the named category with categoryDefaults merged in
// and its repo_root defaulted to reposRoot/<name> when left unset.
// User-defined categories (from config) take precedence over builtins.
// Returns (category, true) on match, (zero, false) on unknown name.
func resolveCategory(name string) (Category, bool) {
	c, ok := configCategories[name]
	if !ok {
		c, ok = builtinCategories()[name]
	}
	if !ok {
		return Category{}, false
	}
	c = mergeCategory(c, categoryDefaults)
	if c.RepoRoot == "" {
		// Default: base repo_root joined with the category name.
		c.RepoRoot = filepath.Join(reposRoot, name)
	}
	return c, true
}

// mergeCategory fills empty fields in c from defaults.
func mergeCategory(c, defaults Category) Category {
	if c.RepoRoot == "" {
		c.RepoRoot = defaults.RepoRoot
	}
	if c.GHAuth == "" {
		c.GHAuth = defaults.GHAuth
	}
	if c.GitProtocol == "" {
		c.GitProtocol = defaults.GitProtocol
	}
	if c.GLabHost == "" {
		c.GLabHost = defaults.GLabHost
	}
	return c
}

// knownCategoryNames returns a sorted list of all resolvable category names
// (config + builtin), for error messages.
func knownCategoryNames() []string {
	seen := map[string]struct{}{}
	for name := range configCategories {
		seen[name] = struct{}{}
	}
	for name := range builtinCategories() {
		seen[name] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// expandCategoryTemplates expands {.category.Name} self-references in the
// category's fields and applies expandHome to RepoRoot.
func expandCategoryTemplates(c Category, name string) (Category, error) {
	selfCtx := map[string]any{"category": map[string]any{"Name": name}}
	for _, field := range []*string{&c.RepoRoot, &c.GHAuth, &c.GitProtocol, &c.GLabHost} {
		rendered, err := tmpl.Render(*field, selfCtx)
		if err != nil {
			return c, fmt.Errorf("expand category %s: %w", name, err)
		}
		*field = rendered
	}
	c.RepoRoot = expandHome(c.RepoRoot)
	return c, nil
}

// repoPlacementPath returns the absolute path where a repo's canonical clone
// should live, per the resolved repo_pattern and the category's RepoRoot.
// info must carry Owner and Name (from parseRemoteURL); Host is available for
// custom patterns. cloneURL is passed to resolveDefaultBranch when the pattern
// references {.branch}.
func repoPlacementPath(catName string, cat Category, info repoInfo, cloneURL string) (string, error) {
	if info.Name == "" || info.Owner == "" {
		return "", fmt.Errorf("cannot derive placement: need owner/name (got owner=%q name=%q); pass an explicit destination", info.Owner, info.Name)
	}
	cat, err := expandCategoryTemplates(cat, catName)
	if err != nil {
		return "", err
	}
	if cat.RepoRoot == "" {
		return "", fmt.Errorf("category %q has no repo_root", catName)
	}
	pattern := repoPattern
	if pattern == "" {
		pattern = defaultRepoPattern
	}

	sep := worktreeSeparator

	branch := ""
	if strings.Contains(pattern, "{.branch}") {
		branch = resolveDefaultBranch(cloneURL)
	}

	ctx := map[string]any{
		"category": map[string]any{"Name": catName, "RepoRoot": cat.RepoRoot},
		"repo":     map[string]any{"Host": info.Host, "Owner": tmpl.Transform(sep, info.Owner), "Name": info.Name},
		"branch":   tmpl.Transform(sep, branch),
		"env":      tmpl.EnvMap(sep),
	}
	rendered, err := tmpl.Render(pattern, ctx)
	if err != nil {
		return "", err
	}
	return filepath.Clean(expandHome(filepath.FromSlash(rendered))), nil
}

// resolveDefaultBranch queries the remote's default branch via git ls-remote,
// which is authoritative for every source (GitHub, GitLab, local, file://).
// Falls back to "main" if the remote is unreachable or HEAD is unresolvable.
func resolveDefaultBranch(cloneURL string) string {
	if cloneURL == "" {
		return "main"
	}
	c := exec.Command("git", "ls-remote", "--symref", cloneURL, "HEAD")
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

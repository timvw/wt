package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var cloneCmd = &cobra.Command{
	Use:     "clone <category> <owner/repo|url> [dest]",
	Aliases: []string{"cl"},
	Short:   "Clone a repository into a category's canonical location",
	Long: `Clone a repository into the canonical location for a category.

Unlike the worktree commands, clone acquires the main repository itself. The
clone is placed under the category's repo_root using an owner/repo/branch layout
(where branch is the remote's default branch) and left on that branch, ready to
inspect. A worktree can be added later with the usual wt commands.

The <owner/repo> form is resolved to a clone URL via gh (or glab), honoring the
category's git_protocol. A full git URL is used as-is. Pass an explicit [dest]
to override placement.`,
	Args: cobra.RangeArgs(2, 3),
	RunE: runClone,
}

func runClone(cmd *cobra.Command, args []string) error {
	categoryName := args[0]
	src := args[1]
	dest := ""
	if len(args) > 2 {
		dest = args[2]
	}

	cat, ok := resolveCategory(categoryName)
	if !ok {
		return fmt.Errorf("unknown category %q (known: %s)", categoryName, strings.Join(knownCategoryNames(), ", "))
	}
	cat, err := expandCategoryTemplates(cat, categoryName)
	if err != nil {
		return err
	}

	// Switch auth context before resolving/cloning. Best-effort: a failure here
	// (e.g. account not configured) is a warning, not fatal.
	if cat.GHAuth != "" {
		if _, err := exec.LookPath("gh"); err == nil {
			if err := ghAuthSwitch(cat.GHAuth); err != nil {
				fmt.Fprintf(os.Stderr, "⚠ gh auth switch to %q failed: %v (continuing with current account)\n", cat.GHAuth, err)
			}
		}
	}

	url, err := resolveCloneURL(src, cat)
	if err != nil {
		return err
	}

	// repoInfo drives placement (host/owner/repo) and hook env. Local paths and
	// unusual URLs may not parse; that's fine when an explicit dest is given.
	info, _ := parseRemoteURL(url)

	var target string
	if dest != "" {
		target = filepath.Clean(expandHome(dest))
	} else {
		target, err = repoPlacementPath(categoryName, cat, info, url)
		if err != nil {
			return err
		}
	}

	if nonEmpty, err := dirExistsNonEmpty(target); err != nil {
		return err
	} else if nonEmpty {
		return fmt.Errorf("destination %s already exists and is not empty", target)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create parent dir for %s: %w", target, err)
	}

	hookEnv := buildHookEnv(info, "", target)
	if err := runHooks("pre_clone", getHooks("pre_clone"), hookEnv); err != nil {
		return fmt.Errorf("pre-clone hook failed: %w", err)
	}

	c := exec.Command("git", "clone", url, target)
	if !isJSONOutput() {
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
	} else {
		c.Stdout = os.Stderr
		c.Stderr = os.Stderr
	}
	if err := c.Run(); err != nil {
		return fmt.Errorf("clone %s: %w", url, err)
	}

	_ = runHooks("post_clone", getHooks("post_clone"), hookEnv)

	if isJSONOutput() {
		return emitJSONSuccess(cmd, map[string]any{
			"status":      "cloned",
			"category":    categoryName,
			"url":         url,
			"path":        target,
			"navigate_to": target,
		})
	}

	fmt.Printf("✓ Cloned to: %s\n", target)
	printCDMarker(target)
	return nil
}

// resolveCloneURL turns the clone source into a git URL. A full URL (scheme or
// scp-like host:path) is used verbatim; an owner/repo shorthand is resolved via
// gh/glab honoring the category's git_protocol.
func resolveCloneURL(src string, cat Category) (string, error) {
	if !looksLikeGHNameWithOwner(src) {
		return src, nil
	}
	proto := cat.GitProtocol
	if proto == "" {
		proto = ghGitProtocol()
	}
	if url := resolveOwnerRepoURL(src, proto, cat.GLabHost); url != "" {
		return url, nil
	}
	return "", fmt.Errorf("could not resolve %q to a clone URL via gh or glab; pass a full git URL", src)
}

// dirExistsNonEmpty reports whether path exists and contains entries.
func dirExistsNonEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("check destination %s: %w", path, err)
	}
	return len(entries) > 0, nil
}

// looksLikeGHNameWithOwner reports whether name is a bare "owner/repo" (one
// slash, no scheme, host separator, or path prefix).
func looksLikeGHNameWithOwner(name string) bool {
	if strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../") ||
		strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~") ||
		strings.Contains(name, ":") {
		return false
	}
	parts := strings.Split(name, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

// resolveOwnerRepoURL resolves an owner/repo to a clone URL, trying gh first
// then glab. Returns "" when neither can resolve it.
func resolveOwnerRepoURL(nameWithOwner, proto, glabHost string) string {
	if _, err := exec.LookPath("gh"); err == nil {
		if url := resolveGHRepoURL(nameWithOwner, proto); url != "" {
			return url
		}
	}
	if _, err := exec.LookPath("glab"); err == nil {
		return resolveGLabRepoURL(nameWithOwner, glabHost)
	}
	return ""
}

// resolveGHRepoURL asks gh for a clone URL, choosing ssh or https per proto.
// gh dropped the httpCloneUrl field; for https we use the .url field (web URL)
// and append ".git" to form a valid clone URL.
func resolveGHRepoURL(nameWithOwner, proto string) string {
	var jqExpr string
	if proto == "https" {
		jqExpr = `.url + ".git"`
	} else {
		jqExpr = ".sshUrl"
	}
	c := exec.Command("gh", "repo", "view", nameWithOwner, "--json", "sshUrl,url", "--jq", jqExpr)
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ghGitProtocol reads gh's configured git_protocol, defaulting to ssh.
func ghGitProtocol() string {
	c := exec.Command("gh", "config", "get", "git_protocol")
	out, err := c.Output()
	if err != nil {
		return "ssh"
	}
	if strings.TrimSpace(string(out)) == "https" {
		return "https"
	}
	return "ssh"
}

// resolveGLabRepoURL asks glab for a clone URL, preferring ssh. host, when set,
// selects the GitLab instance via --hostname.
func resolveGLabRepoURL(nameWithOwner, host string) string {
	args := []string{"repo", "view", nameWithOwner, "--output", "json"}
	if host != "" {
		args = append([]string{"--hostname", host}, args...)
	}
	c := exec.Command("glab", args...)
	out, err := c.Output()
	if err != nil {
		return ""
	}
	var r struct {
		SSHURLToRepo  string `json:"ssh_url_to_repo"`
		HTTPURLToRepo string `json:"http_url_to_repo"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return ""
	}
	if r.SSHURLToRepo != "" {
		return r.SSHURLToRepo
	}
	return r.HTTPURLToRepo
}

// ghAuthSwitch switches the active gh account. No-op on empty account.
func ghAuthSwitch(account string) error {
	if account == "" {
		return nil
	}
	c := exec.Command("gh", "auth", "switch", "--user", account)
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

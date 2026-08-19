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
	Use:     "clone <owner/repo|url> [dest]",
	Aliases: []string{"cl"},
	Short:   "Clone a repository into its canonical location",
	Long: `Clone a repository into its canonical location under repo_root.

Unlike the worktree commands, clone acquires the main repository itself. The
clone is placed using repo_pattern (by default host/owner/repo/default-branch)
and left on its default branch, ready to inspect. A worktree can be added later
with the usual wt commands.

The <owner/repo> form is resolved to a clone URL via gh (or glab). A full git
URL is used as-is. Pass an explicit [dest] to override placement.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runClone,
}

func runClone(cmd *cobra.Command, args []string) error {
	src := args[0]
	dest := ""
	if len(args) > 1 {
		dest = args[1]
	}

	url, err := resolveCloneURL(src)
	if err != nil {
		return err
	}

	// repoInfo drives placement (host/owner/repo) and hook env. Local paths and
	// unusual URLs may not parse; that's fine when an explicit dest is given.
	info, _ := parseRemoteURL(url)

	// Resolved up front rather than only for {.branch} patterns: clone hooks get
	// it as WT_BRANCH, so an explicit destination needs it too. Falls back to
	// "main" when the remote is unreachable — the clone below then fails anyway.
	branch := resolveDefaultBranch(url)

	var target string
	if dest != "" {
		target = filepath.Clean(expandHome(dest))
	} else {
		target, err = repoPlacementPath(info, branch)
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

	// The clone is its own main worktree, so WT_MAIN and WT_PATH are the same
	// path here. parseRemoteURL never sets Main (it only reads the URL), so it
	// has to be filled in explicitly or hooks would see an empty WT_MAIN.
	info.Main = target
	hookEnv := buildHookEnv(info, branch, target)
	if err := runHooks("pre_clone", getHooks("pre_clone"), hookEnv); err != nil {
		return fmt.Errorf("pre-clone hook failed: %w", err)
	}

	// "--" keeps a source that begins with "-" from being read as a git option.
	c := exec.Command("git", "clone", "--", url, target)
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
// gh/glab, each honoring its own configured git_protocol.
func resolveCloneURL(src string) (string, error) {
	if !looksLikeGHNameWithOwner(src) {
		return src, nil
	}
	if url := resolveOwnerRepoURL(src); url != "" {
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
// then glab. Each tool's own git_protocol setting decides ssh vs https, so a
// GitLab-only user is not held to whatever gh happens to be configured for.
// Returns "" when neither can resolve it.
func resolveOwnerRepoURL(nameWithOwner string) string {
	if _, err := exec.LookPath("gh"); err == nil {
		if url := resolveGHRepoURL(nameWithOwner, gitProtocol("gh")); url != "" {
			return url
		}
	}
	if _, err := exec.LookPath("glab"); err == nil {
		return resolveGLabRepoURL(nameWithOwner, gitProtocol("glab"))
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

// gitProtocol reads the git_protocol setting of gh or glab (both spell the
// command the same way), defaulting to ssh when the tool is absent or unset.
func gitProtocol(tool string) string {
	out, err := exec.Command(tool, "config", "get", "git_protocol").Output()
	if err != nil {
		return "ssh"
	}
	if strings.TrimSpace(string(out)) == "https" {
		return "https"
	}
	return "ssh"
}

// resolveGLabRepoURL asks glab for a clone URL, honoring proto the same way the
// gh path does rather than always preferring ssh.
func resolveGLabRepoURL(nameWithOwner, proto string) string {
	c := exec.Command("glab", "repo", "view", nameWithOwner, "--output", "json")
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
	primary, fallback := r.SSHURLToRepo, r.HTTPURLToRepo
	if proto == "https" {
		primary, fallback = r.HTTPURLToRepo, r.SSHURLToRepo
	}
	if primary != "" {
		return primary
	}
	return fallback
}

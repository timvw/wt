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

	// Checked on both routes into target. repoPlacementPath already refuses a
	// *pattern* that renders here and offers an explicit destination as the way
	// out — but the way out is a different path, not permission to name this one.
	// What lands in wt's config directory becomes wt's config file and approval
	// store: a committed trust.toml is a set of approvals the user never gave,
	// and it would cover every repository on the machine. Typing the path rather
	// than rendering it does not change what the repository would be supplying.
	if owned := wtStateAtPath(target); owned != "" {
		return fmt.Errorf(
			"this clone would land at %s, which is %s.\n"+
				"The files cloned there would become wt's own config file and approval store,\n"+
				"which is what decides whether any repository's hooks run.\n"+
				"Clone it somewhere else — a config file symlinked from there still applies",
			target, owned)
	}

	if nonEmpty, err := dirExistsNonEmpty(target); err != nil {
		return err
	} else if nonEmpty {
		return fmt.Errorf("destination %s already exists and is not empty", target)
	}

	// Nothing is at target — which is what says any approval recorded for it was
	// given to a repository that is no longer there. Discard those before a
	// different repository moves in, or it inherits them: an approval is pinned
	// to (scope, sha256 of the commands), so an attacker who takes over an
	// abandoned namespace, or a repo_pattern that renders onto a path an old
	// checkout occupied, arrives pre-approved by declaring a command the user
	// once approved there.
	//
	// Done here rather than after the clone because what makes the records stale
	// is that the path is empty, and that is what was just established. A clone
	// that then fails leaves the user's approvals no less correct.
	dropped, err := dropTrustRecordsAt(target)
	if err != nil {
		return err
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
			"status":                  "cloned",
			"url":                     url,
			"path":                    target,
			"navigate_to":             target,
			"stale_approvals_dropped": dropped,
		})
	}

	fmt.Printf("✓ Cloned to: %s\n", target)
	// Said out loud, because it is the one case where a repository the user has
	// approved before will ask again: the approval belonged to whatever used to
	// be at this path, not to what was just cloned into it.
	if dropped > 0 {
		fmt.Printf("  Note: discarded %d hook approval(s) left at this path by a repository that is no longer there.\n", dropped)
	}
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

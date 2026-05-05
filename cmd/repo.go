package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type repoInfo struct {
	Main  string
	Host  string
	Owner string
	Name  string
}

func getDefaultBase() string {
	cmd := exec.Command("git", "symbolic-ref", "refs/remotes/origin/HEAD")
	output, err := cmd.Output()
	if err != nil {
		return "main"
	}
	ref := strings.TrimSpace(string(output))
	return strings.TrimPrefix(ref, "refs/remotes/origin/")
}

func getRepoInfo() (repoInfo, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	var repoRoot string
	isBare := false
	if err == nil {
		repoRoot = strings.TrimSpace(string(output))
	} else {
		cmd = exec.Command("git", "rev-parse", "--is-bare-repository")
		output, err = cmd.Output()
		if err != nil || strings.TrimSpace(string(output)) != "true" {
			return repoInfo{}, fmt.Errorf("not in a git repository")
		}
		isBare = true
		cmd = exec.Command("git", "rev-parse", "--absolute-git-dir")
		output, err = cmd.Output()
		if err != nil {
			return repoInfo{}, fmt.Errorf("not in a git repository")
		}
		repoRoot = strings.TrimSpace(string(output))
	}
	repoName := ""
	remoteURL := ""
	cmd = exec.Command("git", "remote", "get-url", "origin")
	output, err = cmd.Output()
	if err == nil {
		remoteURL = strings.TrimSpace(string(output))
		if parsed, ok := parseRemoteURL(remoteURL); ok {
			repoName = parsed.Name
		}
	}
	if repoName == "" {
		repoName = strings.TrimSuffix(filepath.Base(repoRoot), ".git")
		if commonCmd := exec.Command("git", "rev-parse", "--git-common-dir"); commonCmd != nil {
			if commonOutput, commonErr := commonCmd.Output(); commonErr == nil {
				commonDir := strings.TrimSpace(string(commonOutput))
				if commonDir != "" {
					if !filepath.IsAbs(commonDir) {
						commonDir = filepath.Join(repoRoot, commonDir)
					}
					commonDir = filepath.Clean(commonDir)
					base := filepath.Base(commonDir)
					if base == ".git" {
						repoName = filepath.Base(filepath.Dir(commonDir))
					} else {
						repoName = strings.TrimSuffix(base, ".git")
					}
				}
			}
		}
	}
	info := repoInfo{
		Main: getMainWorktreePath(getDefaultBase(), repoName, repoRoot, isBare),
		Name: repoName,
	}

	if remoteURL != "" {
		if parsed, ok := parseRemoteURL(remoteURL); ok {
			info.Host = parsed.Host
			info.Owner = parsed.Owner
		}
	}

	return info, nil
}

func getMainWorktreePath(defaultBranch, repoName, repoRoot string, isBare bool) string {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	output, err := cmd.Output()
	if err == nil {
		type entry struct {
			path   string
			branch string
		}
		var entries []entry
		var current entry
		for _, line := range strings.Split(string(output), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "worktree ") {
				if current.path != "" {
					entries = append(entries, current)
				}
				current = entry{path: strings.TrimPrefix(line, "worktree ")}
				continue
			}
			if strings.HasPrefix(line, "branch ") {
				current.branch = strings.TrimPrefix(line, "branch ")
			}
		}
		if current.path != "" {
			entries = append(entries, current)
		}
		if defaultBranch != "" {
			target := "refs/heads/" + defaultBranch
			for _, e := range entries {
				if e.branch == target {
					return e.path
				}
			}
		}
		for _, e := range entries {
			if filepath.Base(e.path) == repoName {
				return e.path
			}
		}
		for _, e := range entries {
			if stat, err := os.Stat(filepath.Join(e.path, ".git")); err == nil && stat.IsDir() {
				return e.path
			}
		}
		if len(entries) > 0 {
			return entries[0].path
		}
	}

	if isBare {
		return filepath.Join(filepath.Dir(repoRoot), repoName)
	}
	return repoRoot
}

func parseRemoteURL(remoteURL string) (repoInfo, bool) {
	trimmed := strings.TrimSpace(remoteURL)
	if trimmed == "" {
		return repoInfo{}, false
	}

	if strings.Contains(trimmed, "://") {
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Hostname() == "" {
			return repoInfo{}, false
		}
		host := parsed.Hostname()
		path := strings.Trim(parsed.Path, "/")
		parts := strings.Split(path, "/")
		if len(parts) < 2 {
			return repoInfo{}, false
		}
		repo := strings.TrimSuffix(parts[len(parts)-1], ".git")
		owner := strings.Join(parts[:len(parts)-1], "/")
		return repoInfo{Host: host, Owner: owner, Name: repo}, true
	}

	if scpLike := strings.SplitN(trimmed, ":", 2); len(scpLike) == 2 {
		hostPart := scpLike[0]
		path := scpLike[1]
		if atIdx := strings.LastIndex(hostPart, "@"); atIdx != -1 {
			hostPart = hostPart[atIdx+1:]
		}
		host := hostPart
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) < 2 {
			return repoInfo{}, false
		}
		repo := strings.TrimSuffix(parts[len(parts)-1], ".git")
		owner := strings.Join(parts[:len(parts)-1], "/")
		return repoInfo{Host: host, Owner: owner, Name: repo}, true
	}

	return repoInfo{}, false
}

type RemoteType int

const (
	RemoteGitHub RemoteType = iota
	RemoteGitLab
	RemoteUnknown
)

func getPRNumber(input string) (string, error) {
	githubRegex := regexp.MustCompile(`^https://github\.com/.*/pull/([0-9]+)`)
	if matches := githubRegex.FindStringSubmatch(input); matches != nil {
		return matches[1], nil
	}

	gitlabRegex := regexp.MustCompile(`^https://gitlab\.com/.*/-/merge_requests/([0-9]+)`)
	if matches := gitlabRegex.FindStringSubmatch(input); matches != nil {
		return matches[1], nil
	}

	numRegex := regexp.MustCompile(`^[0-9]+$`)
	if numRegex.MatchString(input) {
		return input, nil
	}

	return "", fmt.Errorf("invalid PR/MR number or URL: %s", input)
}

func worktreeExists(branch string) (string, bool) {
	entries, err := getWorktreeListPorcelain()
	if err != nil {
		return "", false
	}

	return findWorktreeByBranch(entries, branch, filesystemCaseInsensitive(".") || filesystemCaseInsensitive(worktreeRoot))
}

func findWorktreeByBranch(entries []worktreeListEntry, branch string, caseInsensitive bool) (string, bool) {
	if branch == "" {
		return "", false
	}

	for _, entry := range entries {
		if entry.Branch == branch {
			return entry.Path, true
		}
	}

	if caseInsensitive {
		for _, entry := range entries {
			if strings.EqualFold(entry.Branch, branch) {
				return entry.Path, true
			}
		}
	}

	return "", false
}

func branchExists(branch string) bool {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", branch))
	if cmd.Run() == nil {
		return true
	}

	cmd = exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/remotes/origin/%s", branch))
	return cmd.Run() == nil
}

// matchRemoteBranch parses git ls-remote --heads output and returns the
// correctly-cased branch name if a case-insensitive match is found.
func matchRemoteBranch(branch string, lsRemoteOutput string) string {
	lowerBranch := strings.ToLower(branch)
	for _, line := range strings.Split(lsRemoteOutput, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		ref := strings.TrimPrefix(parts[1], "refs/heads/")
		if strings.ToLower(ref) == lowerBranch {
			return ref
		}
	}
	return branch
}

// resolveRemoteBranchCase calls git ls-remote and resolves the branch name
// using case-insensitive matching.
func resolveRemoteBranchCase(branch string) string {
	cmd := exec.Command("git", "ls-remote", "--heads", "origin")
	output, err := cmd.Output()
	if err != nil {
		return branch
	}
	return matchRemoteBranch(branch, string(output))
}

func getAvailableBranches() ([]string, error) {
	cmd := exec.Command("git", "branch", "-a", "--format=%(refname:short)")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	branchMap := make(map[string]bool)

	for _, line := range strings.Split(string(output), "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" {
			continue
		}

		if strings.HasPrefix(branch, "origin/HEAD") || strings.Contains(branch, "->") || strings.Contains(branch, "HEAD") {
			continue
		}

		branch = strings.TrimPrefix(branch, "origin/")

		if branch == "origin" || branch == "upstream" {
			continue
		}

		branchMap[branch] = true
	}

	branches := []string{}
	for branch := range branchMap {
		branches = append(branches, branch)
	}

	return branches, nil
}

func getExistingWorktreeBranches() ([]string, error) {
	cmd := exec.Command("git", "worktree", "list")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	branches := []string{}
	lines := strings.Split(string(output), "\n")
	for _, line := range lines[1:] { // Skip first line (main worktree)
		if line == "" {
			continue
		}
		if matches := regexp.MustCompile(`\[([^\]]+)\]`).FindStringSubmatch(line); matches != nil {
			branches = append(branches, matches[1])
		}
	}
	return branches, nil
}

func getMergedBranches(base string) ([]string, error) {
	cmd := exec.Command("git", "branch", "--merged", base, "--format=%(refname:short)")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get merged branches: %w", err)
	}

	var branches []string
	for _, line := range strings.Split(string(output), "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" || branch == base || branch == "main" || branch == "master" {
			continue
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

func parsePROutput(output string) ([]string, []string) {
	var numbers []string
	var labels []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			numbers = append(numbers, parts[0])
			labels = append(labels, fmt.Sprintf("#%s: %s", parts[0], parts[1]))
		}
	}
	return numbers, labels
}

func getOpenPRs() ([]string, []string, error) {
	cmd := exec.Command("gh", "pr", "list", "--json", "number,title", "--jq", ".[] | \"\\(.number)\\t\\(.title)\"")
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, err
	}

	numbers, labels := parsePROutput(string(output))
	return numbers, labels, nil
}

func parseMROutput(output string) ([]string, []string) {
	var numbers []string
	var labels []string
	mrRegex := regexp.MustCompile(`^!(\d+)\s+[^\s]+\s+(.+?)\s+\(`)
	for _, line := range strings.Split(output, "\n") {
		if matches := mrRegex.FindStringSubmatch(line); matches != nil {
			numbers = append(numbers, matches[1])
			labels = append(labels, fmt.Sprintf("!%s: %s", matches[1], strings.TrimSpace(matches[2])))
		}
	}
	return numbers, labels
}

func getOpenMRs() ([]string, []string, error) {
	cmd := exec.Command("glab", "mr", "list")
	output, err := cmd.Output()
	if err != nil {
		return nil, nil, err
	}

	numbers, labels := parseMROutput(string(output))
	return numbers, labels, nil
}

// parseGitHubBranchName extracts the branch name from gh pr view JSON output.
func parseGitHubBranchName(jsonOutput string) (string, error) {
	var result struct {
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &result); err != nil {
		return "", fmt.Errorf("failed to parse GitHub PR JSON: %w", err)
	}
	if result.HeadRefName == "" {
		return "", fmt.Errorf("empty branch name in GitHub PR response")
	}
	return result.HeadRefName, nil
}

// parseGitLabBranchName extracts the branch name from glab mr view JSON output.
func parseGitLabBranchName(jsonOutput string) (string, error) {
	var result struct {
		SourceBranch string `json:"source_branch"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &result); err != nil {
		return "", fmt.Errorf("failed to parse GitLab MR JSON: %w", err)
	}
	if result.SourceBranch == "" {
		return "", fmt.Errorf("empty branch name in GitLab MR response")
	}
	return result.SourceBranch, nil
}

// getPRBranchName looks up the actual branch name for a PR/MR using the gh/glab CLI.
func getPRBranchName(prNumber string, remoteType RemoteType) (string, error) {
	switch remoteType {
	case RemoteGitHub:
		cmd := exec.Command("gh", "pr", "view", prNumber, "--json", "headRefName")
		output, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("failed to get PR branch name: %w", err)
		}
		return parseGitHubBranchName(string(output))
	case RemoteGitLab:
		cmd := exec.Command("glab", "mr", "view", prNumber, "--output", "json")
		output, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("failed to get MR branch name: %w", err)
		}
		return parseGitLabBranchName(string(output))
	default:
		return "", fmt.Errorf("invalid remote type")
	}
}

type worktreeListEntry struct {
	Path     string `json:"path"`
	HEAD     string `json:"head,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Bare     bool   `json:"bare,omitempty"`
	Detached bool   `json:"detached,omitempty"`
	Locked   string `json:"locked,omitempty"`
	Prunable string `json:"prunable,omitempty"`
}

func getWorktreeListPorcelain() ([]worktreeListEntry, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	entries := make([]worktreeListEntry, 0)
	current := worktreeListEntry{}

	for _, rawLine := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			if current.Path != "" {
				entries = append(entries, current)
				current = worktreeListEntry{}
			}
			continue
		}

		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				entries = append(entries, current)
			}
			current = worktreeListEntry{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			branch := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(branch, "refs/heads/")
		case line == "bare":
			current.Bare = true
		case line == "detached":
			current.Detached = true
		case strings.HasPrefix(line, "locked"):
			current.Locked = strings.TrimSpace(strings.TrimPrefix(line, "locked"))
		case strings.HasPrefix(line, "prunable"):
			current.Prunable = strings.TrimSpace(strings.TrimPrefix(line, "prunable"))
		}
	}

	if current.Path != "" {
		entries = append(entries, current)
	}

	return entries, nil
}

func isDirEmpty(path string) (bool, error) {
	dir, err := os.Open(path)
	switch {
	case os.IsNotExist(err):
		return true, nil
	case err != nil:
		return false, err
	}
	defer func() { _ = dir.Close() }()

	_, err = dir.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

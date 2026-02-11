package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"text/template"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var (
	version          = "dev"
	worktreeRoot     string
	worktreeStrategy string
	worktreePattern  string
)

func init() {
	loadWorktreeConfig()
	rootCmd.Long = buildRootCmdLong()
}

func main() {
	// Re-load config after cobra parses flags so --config is available
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if configFlag != "" {
			loadWorktreeConfig()
			rootCmd.Long = buildRootCmdLong()
		}
	}
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "wt",
	Short: "Git worktree helper with organized directory structure",
	Long:  "",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configFlag, "config", "", "Path to config file (default: ~/.config/wt/config.toml)")
	rootCmd.AddCommand(checkoutCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(mrCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(pruneCmd)
	rootCmd.AddCommand(cleanupCmd)
	rootCmd.AddCommand(shellenvCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(configCmd)
	removeCmd.Flags().BoolVarP(&removeForce, "force", "f", false, "Force removal even if worktree has modifications")
	cleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "Preview what would be removed without making changes")
	cleanupCmd.Flags().BoolVarP(&cleanupForce, "force", "f", false, "Remove all merged worktrees without confirmation")
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Preview changes without modifying files")
	initCmd.Flags().BoolVar(&initUninstall, "uninstall", false, "Remove wt configuration from shell")
	initCmd.Flags().BoolVar(&initNoPrompt, "no-prompt", false, "Skip activation instructions (for automated installs)")
	configInitCmd.Flags().BoolVar(&configInitForce, "force", false, "Overwrite existing config file")
}

// Helper functions

type repoInfo struct {
	Main  string
	Host  string
	Owner string
	Name  string
}

// loadWorktreeConfig is defined in config.go

func buildRootCmdLong() string {
	pattern, err := resolveWorktreePattern()
	if err != nil {
		pattern = worktreePattern
		if pattern == "" {
			pattern = "unknown"
		}
	}

	return fmt.Sprintf(`Git-like worktree management with organized directory structure.

Strategy: %s
Pattern:  %s
Root:     %s

Run 'wt info' to see available strategies and pattern variables.
Set WORKTREE_ROOT, WORKTREE_STRATEGY, and WORKTREE_PATTERN to customize.`,
		worktreeStrategy,
		pattern,
		worktreeRoot,
	)
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
	// Check if it's a GitHub PR URL
	githubRegex := regexp.MustCompile(`^https://github\.com/.*/pull/([0-9]+)`)
	if matches := githubRegex.FindStringSubmatch(input); matches != nil {
		return matches[1], nil
	}

	// Check if it's a GitLab MR URL
	gitlabRegex := regexp.MustCompile(`^https://gitlab\.com/.*/-/merge_requests/([0-9]+)`)
	if matches := gitlabRegex.FindStringSubmatch(input); matches != nil {
		return matches[1], nil
	}

	// Check if it's just a number
	numRegex := regexp.MustCompile(`^[0-9]+$`)
	if numRegex.MatchString(input) {
		return input, nil
	}

	return "", fmt.Errorf("invalid PR/MR number or URL: %s", input)
}

func worktreeExists(branch string) (string, bool) {
	cmd := exec.Command("git", "worktree", "list")
	output, err := cmd.Output()
	if err != nil {
		return "", false
	}

	lines := strings.Split(string(output), "\n")
	searchPattern := fmt.Sprintf("[%s]", branch)
	for _, line := range lines {
		if strings.Contains(line, searchPattern) {
			// Extract the path (first field)
			fields := strings.Fields(line)
			if len(fields) > 0 {
				return fields[0], true
			}
		}
	}
	return "", false
}

func branchExists(branch string) bool {
	// Check local branch
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", branch))
	if cmd.Run() == nil {
		return true
	}

	// Check remote branch
	cmd = exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/remotes/origin/%s", branch))
	return cmd.Run() == nil
}

func buildWorktreePath(info repoInfo, branch string) (string, error) {
	pattern, err := resolveWorktreePattern()
	if err != nil {
		return "", err
	}

	envMap := map[string]string{}
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	context := map[string]any{
		"repo":         info,
		"branch":       branch,
		"branchSafe":   strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(branch, "/", "-"), "\\", "-")),
		"worktreeRoot": worktreeRoot,
		"env":          envMap,
	}

	if pattern == "" {
		return "", fmt.Errorf("worktree pattern cannot be empty")
	}

	tpl, err := template.New("worktreePattern").
		Delims("{", "}").
		Option("missingkey=error").
		Parse(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid worktree pattern: %w", err)
	}

	var renderedBuf bytes.Buffer
	if err := tpl.Execute(&renderedBuf, context); err != nil {
		return "", fmt.Errorf("pattern variables missing values: %w", err)
	}

	rendered := renderedBuf.String()
	rendered = filepath.FromSlash(rendered)
	if !filepath.IsAbs(rendered) {
		rendered = filepath.Join(worktreeRoot, rendered)
	}

	rendered = filepath.Clean(rendered)
	parent := filepath.Dir(rendered)
	infoStat, err := os.Stat(parent)
	switch {
	case err == nil:
		if !infoStat.IsDir() {
			return "", fmt.Errorf("worktree path %s is not a directory", parent)
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", fmt.Errorf("failed to create worktree directory %s: %w", parent, err)
		}
	default:
		return "", fmt.Errorf("failed to access worktree directory %s: %w", parent, err)
	}

	return rendered, nil
}

func cleanupWorktreePath(worktreePath string) error {
	if worktreePath == "" {
		return nil
	}

	if err := os.RemoveAll(worktreePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove worktree directory %s: %w", worktreePath, err)
	}

	absRoot, err := filepath.Abs(worktreeRoot)
	if err != nil {
		return nil
	}

	absWorktreePath, err := filepath.Abs(worktreePath)
	if err != nil {
		return nil
	}

	repoDir := filepath.Dir(absWorktreePath)
	if strings.HasPrefix(repoDir, absRoot) {
		if empty, err := isDirEmpty(repoDir); err == nil && empty {
			_ = os.Remove(repoDir)
		}
	}

	return nil
}

func resolveWorktreePattern() (string, error) {
	if worktreePattern != "" {
		return worktreePattern, nil
	}
	if worktreeStrategy == "custom" {
		return "", fmt.Errorf("WORKTREE_PATTERN is required when WORKTREE_STRATEGY is 'custom'")
	}

	switch worktreeStrategy {
	case "global":
		return "{.worktreeRoot}/{.repo.Name}/{.branch}", nil
	case "sibling-repo", "sibling":
		return "{.repo.Main}/../{.repo.Name}-{.branchSafe}", nil
	case "parent-worktrees", "parent-centered":
		return "{.repo.Main}/../{.repo.Name}.worktrees/{.branch}", nil
	case "parent-branches", "repo-root":
		return "{.repo.Main}/../{.branch}", nil
	case "parent-dotdir", "local-root":
		return "{.repo.Main}/../.worktrees/{.branch}", nil
	case "inside-dotdir", "nested-local":
		return "{.repo.Main}/.worktrees/{.branch}", nil
	default:
		return "", fmt.Errorf("unsupported WORKTREE_STRATEGY: %s", worktreeStrategy)
	}
}

func isDirEmpty(path string) (bool, error) {
	dir, err := os.Open(path)
	switch {
	case os.IsNotExist(err):
		return true, nil
	case err != nil:
		return false, err
	}
	defer dir.Close()

	_, err = dir.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	return false, err
}

func printCDMarker(path string) {
	fmt.Printf("wt navigating to: %s\n", path)
}

func getAvailableBranches() ([]string, error) {
	// Get local and remote branches
	cmd := exec.Command("git", "branch", "-a", "--format=%(refname:short)")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	// Use a map to deduplicate
	branchMap := make(map[string]bool)

	for _, line := range strings.Split(string(output), "\n") {
		branch := strings.TrimSpace(line)
		if branch == "" {
			continue
		}

		// Skip remote HEAD pointers and detached HEAD states
		if strings.HasPrefix(branch, "origin/HEAD") || strings.Contains(branch, "->") || strings.Contains(branch, "HEAD") {
			continue
		}

		// For remote branches, strip the origin/ prefix
		branch = strings.TrimPrefix(branch, "origin/")

		// Skip if branch name is just "origin" or other remote names
		if branch == "origin" || branch == "upstream" {
			continue
		}

		// Add to map (deduplicates automatically)
		branchMap[branch] = true
	}

	// Convert map to slice
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
		// Extract branch name from [branch] format
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
		// Skip empty lines and base branches
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
	// Parse glab output: !123  title  (branch) ← (target)
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

// Commands

var checkoutCmd = &cobra.Command{
	Use:     "checkout [branch]",
	Aliases: []string{"co"},
	Short:   "Checkout existing branch in new worktree",
	Args:    cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var branch string

		// Interactive selection if no branch provided
		if len(args) == 0 {
			branches, err := getAvailableBranches()
			if err != nil {
				return fmt.Errorf("failed to get branches: %w", err)
			}
			if len(branches) == 0 {
				return fmt.Errorf("no available branches to checkout")
			}

			prompt := promptui.Select{
				Label: "Select branch to checkout",
				Items: branches,
			}
			_, result, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}
			branch = result
		} else {
			branch = args[0]
		}
		info, err := getRepoInfo()
		if err != nil {
			return err
		}

		// Check if worktree already exists
		if existingPath, exists := worktreeExists(branch); exists {
			fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
			printCDMarker(existingPath)
			return nil
		}

		// Check if branch exists
		if !branchExists(branch) {
			return fmt.Errorf("branch '%s' does not exist\nUse 'wt create %s' to create a new branch", branch, branch)
		}

		path, err := buildWorktreePath(info, branch)
		if err != nil {
			return err
		}

		// Create worktree
		gitCmd := exec.Command("git", "worktree", "add", path, branch)
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		fmt.Printf("✓ Worktree created at: %s\n", path)
		printCDMarker(path)
		return nil
	},
}

var createCmd = &cobra.Command{
	Use:   "create <branch> [base-branch]",
	Short: "Create new branch in worktree (default: main/master)",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		branch := args[0]
		base := getDefaultBase()
		if len(args) > 1 {
			base = args[1]
		}

		info, err := getRepoInfo()
		if err != nil {
			return err
		}

		// Check if worktree already exists
		if existingPath, exists := worktreeExists(branch); exists {
			fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
			printCDMarker(existingPath)
			return nil
		}

		path, err := buildWorktreePath(info, branch)
		if err != nil {
			return err
		}

		// Create new branch and worktree
		gitCmd := exec.Command("git", "worktree", "add", path, "-b", branch, base)
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		fmt.Printf("✓ Worktree created at: %s\n", path)
		printCDMarker(path)
		return nil
	},
}

var prCmd = &cobra.Command{
	Use:   "pr [number|url]",
	Short: "Checkout GitHub PR in worktree (uses gh CLI)",
	Long: `Checkout a GitHub Pull Request in a worktree.

Looks up the PR's actual branch name using the 'gh' CLI, then creates
a worktree with that branch name — just like 'wt checkout <branch>'.
For GitLab Merge Requests, use 'wt mr' instead.

Examples:
  wt pr                                        # Interactive PR selection
  wt pr 123                                    # GitHub PR number
  wt pr https://github.com/org/repo/pull/123   # GitHub PR URL`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var input string

		// Interactive selection if no PR provided
		if len(args) == 0 {
			numbers, labels, err := getOpenPRs()
			if err != nil {
				return fmt.Errorf("failed to get PRs: %w (is 'gh' CLI installed?)", err)
			}
			if len(labels) == 0 {
				return fmt.Errorf("no open PRs found")
			}

			prompt := promptui.Select{
				Label: "Select Pull Request",
				Items: labels,
			}
			idx, _, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}
			input = numbers[idx]
		} else {
			input = args[0]
		}

		return checkoutPROrMR(input, RemoteGitHub)
	},
}

var mrCmd = &cobra.Command{
	Use:   "mr [number|url]",
	Short: "Checkout GitLab MR in worktree (uses glab CLI)",
	Long: `Checkout a GitLab Merge Request in a worktree.

Looks up the MR's actual branch name using the 'glab' CLI, then creates
a worktree with that branch name — just like 'wt checkout <branch>'.
For GitHub Pull Requests, use 'wt pr' instead.

Examples:
  wt mr                                        # Interactive MR selection
  wt mr 123                                    # GitLab MR number
  wt mr https://gitlab.com/org/repo/-/merge_requests/123  # GitLab MR URL`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var input string

		// Interactive selection if no MR provided
		if len(args) == 0 {
			numbers, labels, err := getOpenMRs()
			if err != nil {
				return fmt.Errorf("failed to get MRs: %w (is 'glab' CLI installed?)", err)
			}
			if len(labels) == 0 {
				return fmt.Errorf("no open MRs found")
			}

			prompt := promptui.Select{
				Label: "Select Merge Request",
				Items: labels,
			}
			idx, _, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}
			input = numbers[idx]
		} else {
			input = args[0]
		}

		return checkoutPROrMR(input, RemoteGitLab)
	},
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

func checkoutPROrMR(input string, remoteType RemoteType) error {
	prNumber, err := getPRNumber(input)
	if err != nil {
		return err
	}

	var refSpec, prefix string

	switch remoteType {
	case RemoteGitHub:
		refSpec = fmt.Sprintf("pull/%s/head", prNumber)
		prefix = "pr"
		if _, err := exec.LookPath("gh"); err != nil {
			return fmt.Errorf("'gh' CLI not found. Install it from https://cli.github.com")
		}
	case RemoteGitLab:
		refSpec = fmt.Sprintf("merge-requests/%s/head", prNumber)
		prefix = "mr"
		if _, err := exec.LookPath("glab"); err != nil {
			return fmt.Errorf("'glab' CLI not found. Install it from https://gitlab.com/gitlab-org/cli")
		}
	default:
		return fmt.Errorf("invalid remote type")
	}

	// Look up the actual branch name from the PR/MR
	branch, err := getPRBranchName(prNumber, remoteType)
	if err != nil {
		return fmt.Errorf("failed to look up branch for %s #%s: %w", strings.ToUpper(prefix), prNumber, err)
	}

	info, err := getRepoInfo()
	if err != nil {
		return err
	}

	// Check if worktree already exists for this branch
	if existingPath, exists := worktreeExists(branch); exists {
		fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
		printCDMarker(existingPath)
		return nil
	}

	path, err := buildWorktreePath(info, branch)
	if err != nil {
		return err
	}

	// Try fetching the branch directly from origin
	fetchCmd := exec.Command("git", "fetch", "origin", branch)
	fetchCmd.Stderr = os.Stderr
	if err := fetchCmd.Run(); err != nil {
		// Fallback: fetch via PR/MR refspec (e.g. for fork PRs)
		fallbackCmd := exec.Command("git", "fetch", "origin", fmt.Sprintf("%s:%s", refSpec, branch))
		fallbackCmd.Stderr = os.Stderr
		_ = fallbackCmd.Run()
	}

	// Create worktree — prefer the remote-tracking branch, fall back to local
	var gitCmd *exec.Cmd
	if branchExists(branch) {
		gitCmd = exec.Command("git", "worktree", "add", path, branch)
	} else {
		gitCmd = exec.Command("git", "worktree", "add", path, "-b", branch, fmt.Sprintf("origin/%s", branch))
	}
	gitCmd.Stdout = os.Stdout
	gitCmd.Stderr = os.Stderr
	if err := gitCmd.Run(); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	// Ensure upstream tracking is set (best-effort, may fail for fork PRs)
	upstreamCmd := exec.Command("git", "branch", "--set-upstream-to",
		fmt.Sprintf("origin/%s", branch), branch)
	_ = upstreamCmd.Run()

	fmt.Printf("✓ %s #%s (%s) checked out at: %s\n", strings.ToUpper(prefix), prNumber, branch, path)
	printCDMarker(path)
	return nil
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all worktrees",
	Run: func(cmd *cobra.Command, args []string) {
		gitCmd := exec.Command("git", "worktree", "list")
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
		_ = gitCmd.Run()
	},
}

var (
	removeForce   bool
	cleanupDryRun bool
	cleanupForce  bool
)

var removeCmd = &cobra.Command{
	Use:     "remove [branch]",
	Aliases: []string{"rm"},
	Short:   "Remove a worktree",
	Args:    cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var branch string

		// Interactive selection if no branch provided
		if len(args) == 0 {
			branches, err := getExistingWorktreeBranches()
			if err != nil {
				return fmt.Errorf("failed to get worktrees: %w", err)
			}
			if len(branches) == 0 {
				return fmt.Errorf("no worktrees to remove")
			}

			prompt := promptui.Select{
				Label: "Select worktree to remove",
				Items: branches,
			}
			_, result, err := prompt.Run()
			if err != nil {
				return fmt.Errorf("selection cancelled")
			}
			branch = result
		} else {
			branch = args[0]
		}

		existingPath, exists := worktreeExists(branch)
		if !exists {
			return fmt.Errorf("no worktree found for branch: %s", branch)
		}

		// Check if we're currently in the worktree being removed
		cwd, err := os.Getwd()
		inRemovedWorktree := err == nil && strings.HasPrefix(cwd, existingPath)

		// Find the main worktree path (for cd after removal)
		var mainWorktreePath string
		if inRemovedWorktree {
			listCmd := exec.Command("git", "worktree", "list")
			output, err := listCmd.Output()
			if err == nil {
				lines := strings.Split(string(output), "\n")
				if len(lines) > 0 {
					// First line is always the main worktree
					fields := strings.Fields(lines[0])
					if len(fields) > 0 {
						mainWorktreePath = fields[0]
					}
				}
			}
		}

		gitArgs := []string{"worktree", "remove"}
		if removeForce {
			gitArgs = append(gitArgs, "--force")
		}
		gitArgs = append(gitArgs, existingPath)

		gitCmd := exec.Command("git", gitArgs...)
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to remove worktree: %w", err)
		}

		if err := cleanupWorktreePath(existingPath); err != nil {
			return err
		}

		fmt.Printf("✓ Removed worktree: %s\n", existingPath)

		// If we were in the removed worktree, navigate to main
		if inRemovedWorktree && mainWorktreePath != "" {
			printCDMarker(mainWorktreePath)
		}

		return nil
	},
}

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove worktrees for merged branches",
	Long: `Remove worktrees for branches that have been merged into the base branch.

This command finds all worktrees whose branches have been merged into main/master,
and removes them. Use --dry-run to preview what would be removed.

Examples:
  wt cleanup              # Interactive confirmation for each worktree
  wt cleanup --dry-run    # Preview what would be removed
  wt cleanup --force      # Remove all without confirmation`,
	RunE: func(cmd *cobra.Command, args []string) error {
		base := getDefaultBase()

		// Get merged branches
		mergedBranches, err := getMergedBranches(base)
		if err != nil {
			return err
		}

		// Get existing worktree branches
		worktreeBranches, err := getExistingWorktreeBranches()
		if err != nil {
			return fmt.Errorf("failed to get worktrees: %w", err)
		}

		// Create a set of merged branches for quick lookup
		mergedSet := make(map[string]bool)
		for _, b := range mergedBranches {
			mergedSet[b] = true
		}

		// Find worktrees that are for merged branches
		var toRemove []string
		for _, branch := range worktreeBranches {
			if mergedSet[branch] {
				toRemove = append(toRemove, branch)
			}
		}

		if len(toRemove) == 0 {
			fmt.Println("No worktrees found for merged branches")
			return nil
		}

		// Dry run mode - just show what would be removed
		if cleanupDryRun {
			fmt.Printf("Would remove %d worktree(s) for merged branches:\n", len(toRemove))
			for _, branch := range toRemove {
				if path, exists := worktreeExists(branch); exists {
					fmt.Printf("  - %s (%s)\n", branch, path)
				}
			}
			return nil
		}

		// Track results
		removed := 0
		skipped := 0

		for _, branch := range toRemove {
			existingPath, exists := worktreeExists(branch)
			if !exists {
				continue
			}

			// If not force mode, ask for confirmation
			if !cleanupForce {
				prompt := promptui.Prompt{
					Label:     fmt.Sprintf("Remove worktree for merged branch '%s'", branch),
					IsConfirm: true,
				}
				_, err := prompt.Run()
				if err != nil {
					fmt.Printf("  Skipped: %s\n", branch)
					skipped++
					continue
				}
			}

			// Remove the worktree
			gitCmd := exec.Command("git", "worktree", "remove", existingPath)
			gitCmd.Stdout = os.Stdout
			gitCmd.Stderr = os.Stderr
			if err := gitCmd.Run(); err != nil {
				fmt.Printf("  Failed to remove %s: %v\n", branch, err)
				continue
			}

			if err := cleanupWorktreePath(existingPath); err != nil {
				fmt.Printf("  Warning: failed to cleanup path for %s: %v\n", branch, err)
			}

			fmt.Printf("✓ Removed worktree: %s\n", branch)
			removed++
		}

		// Run prune at the end
		pruneGitCmd := exec.Command("git", "worktree", "prune")
		_ = pruneGitCmd.Run()

		fmt.Printf("\nCleanup complete: %d removed, %d skipped\n", removed, skipped)
		return nil
	},
}

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove worktree administrative files",
	Run: func(cmd *cobra.Command, args []string) {
		gitCmd := exec.Command("git", "worktree", "prune")
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
		if err := gitCmd.Run(); err == nil {
			fmt.Println("✓ Pruned stale worktree administrative files")
		}
	},
}

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show worktree location configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern, err := resolveWorktreePattern()
		if err != nil {
			pattern = worktreePattern
			if pattern == "" {
				pattern = "unknown"
			}
		}

		configStatus := "not found, using defaults"
		if configFileFound {
			configStatus = "found"
		}

		fmt.Printf(`Config:   %s (%s)

Strategy: %s
Pattern:  %s
Root:     %s

Strategies:
  global           -> {.worktreeRoot}/{.repo.Name}/{.branch}
  sibling-repo     -> {.repo.Main}/../{.repo.Name}-{.branchSafe}
  parent-branches  -> {.repo.Main}/../{.branch}
  parent-worktrees -> {.repo.Main}/../{.repo.Name}.worktrees/{.branch}
  parent-dotdir    -> {.repo.Main}/../.worktrees/{.branch}
  inside-dotdir    -> {.repo.Main}/.worktrees/{.branch}
  custom           -> requires pattern setting

Pattern variables: {.repo.Name}, {.repo.Main}, {.repo.Owner}, {.repo.Host}, {.branch}, {.branchSafe}, {.worktreeRoot}, {.env.VARNAME}
Note: {.branchSafe} is sanitized for filesystem paths (slashes replaced).
Note: {.env.VARNAME} accesses the environment variable VARNAME (e.g. {.env.HOME}).
`, configFilePath, configStatus, worktreeStrategy, pattern, worktreeRoot)
		return nil
	},
}

var configInitForce bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage wt configuration",
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a default configuration file",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := resolveConfigPath(configFlag)
		if err := writeDefaultConfig(path, configInitForce); err != nil {
			return err
		}
		fmt.Printf("Created config file: %s\n", path)
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show effective configuration with sources",
	Run: func(cmd *cobra.Command, args []string) {
		pattern, err := resolveWorktreePattern()
		if err != nil {
			pattern = worktreePattern
			if pattern == "" {
				pattern = "(none)"
			}
		}

		configStatus := "not found"
		if configFileFound {
			configStatus = "found"
		}

		fmt.Printf("Config file: %s (%s)\n\n", configFilePath, configStatus)
		fmt.Printf("Effective configuration:\n")
		fmt.Printf("  %-10s = %-40s (%s)\n", "root", worktreeRoot, configSources.Root)
		fmt.Printf("  %-10s = %-40s (%s)\n", "strategy", worktreeStrategy, configSources.Strategy)
		if worktreePattern != "" {
			fmt.Printf("  %-10s = %-40s (%s)\n", "pattern", worktreePattern, configSources.Pattern)
		} else {
			fmt.Printf("  %-10s = %-40s (%s)\n", "pattern", pattern, configSources.Pattern)
		}
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(resolveConfigPath(configFlag))
	},
}

func init() {
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configPathCmd)
}

var shellenvCmd = &cobra.Command{
	Use:   "shellenv",
	Short: "Output shell function for auto-cd (source this)",
	Long: `Output shell integration code for automatic directory navigation.

Add this to the END of your ~/.bashrc or ~/.zshrc:
  source <(wt shellenv)

For PowerShell, add this to your $PROFILE:
  Invoke-Expression (& wt shellenv)

Note: For zsh, place this AFTER compinit to enable tab completion.

This enables:
- Automatic cd to worktree after checkout/create/pr/mr commands
- Tab completion for commands and branch names`,
	Run: func(cmd *cobra.Command, args []string) {
		// Output OS-specific shell integration
		// On Windows, default to PowerShell. On Unix, output bash/zsh.
		if runtime.GOOS == "windows" {
			// PowerShell integration for Windows
			fmt.Print(`# PowerShell integration (Windows)
# Detected via runtime.GOOS, compatible with $PSVersionTable
# NOTE: Requires wt.exe to be in PATH or current directory

function wt {
    # Call wt.exe explicitly to avoid recursive function call
    # PowerShell will find wt.exe in PATH or current directory
    $output = & wt.exe @args
    $exitCode = $LASTEXITCODE
    Write-Output $output
    if ($exitCode -eq 0) {
        $cdPath = $output | Select-String -Pattern "^wt navigating to: " | ForEach-Object { $_.Line.Substring(18) }
        if ($cdPath) {
            Set-Location $cdPath
        }
    }
    $global:LASTEXITCODE = $exitCode
}

# PowerShell completion
Register-ArgumentCompleter -CommandName wt -ScriptBlock {
    param($commandName, $wordToComplete, $commandAst, $fakeBoundParameters)

    $commands = @('checkout', 'co', 'create', 'pr', 'mr', 'list', 'ls', 'remove', 'rm', 'cleanup', 'prune', 'help', 'shellenv', 'init', 'info', 'config', 'version')

    # Get the position in the command line
    $position = $commandAst.CommandElements.Count - 1

    if ($position -eq 0) {
        # Complete commands
        $commands | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
            [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
        }
    } elseif ($position -eq 1) {
        $subCommand = $commandAst.CommandElements[1].Value
        if ($subCommand -in @('checkout', 'co', 'remove', 'rm')) {
            # Complete branch names from worktree list
            $branches = git worktree list 2>$null | Select-Object -Skip 1 | ForEach-Object {
                if ($_ -match '\[([^\]]+)\]') { $matches[1] }
            }
            $branches | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
                [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
            }
        } elseif ($subCommand -eq 'config') {
            @('init', 'show', 'path') | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
                [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
            }
        }
    }
}
`)
			return
		}

		// Bash/Zsh integration for Unix systems
		fmt.Print(`wt() {
    # Use script(1) to provide a PTY for interactive commands (e.g., promptui menus)
    # Command substitution $(command wt) doesn't allocate a TTY, which breaks interactive prompts
    local log_file exit_code cd_path
    log_file=$(mktemp -t wt.XXXXXX)

    # Detect OS to use correct script syntax (macOS vs Linux)
    if [ "$(uname)" = "Darwin" ]; then
        # macOS: script -q file command args
        script -q "$log_file" /bin/sh -c 'command wt "$@"' wt "$@"
    else
        # Linux: script -q -c "command wt $*" "$log_file"
        script -q -c "command wt $*" "$log_file"
    fi
    exit_code=$?

    # Extract the navigation marker for auto-cd
    cd_path=$(grep '^wt navigating to: ' "$log_file" | tail -1 | sed 's/^wt navigating to: //')
    rm -f "$log_file"
    cd_path=${cd_path%$'\r'}

    if [ $exit_code -eq 0 ] && [ -n "$cd_path" ]; then
        cd "$cd_path"
    fi
    return $exit_code
}

# Bash completion
if [ -n "$BASH_VERSION" ]; then
    _wt_complete() {
        local cur prev commands
        COMPREPLY=()
        cur="${COMP_WORDS[COMP_CWORD]}"
        prev="${COMP_WORDS[COMP_CWORD-1]}"
        commands="checkout co create pr mr list ls remove rm cleanup prune help shellenv init info config version"

        # Complete commands if first argument
        if [ $COMP_CWORD -eq 1 ]; then
            COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
            return 0
        fi

        # Complete branch names for checkout/remove/rm
        case "$prev" in
            checkout|co|remove|rm)
                local branches
                branches=$(git worktree list 2>/dev/null | tail -n +2 | sed -n 's/.*\[\([^]]*\)\].*/\1/p')
                COMPREPLY=( $(compgen -W "$branches" -- "$cur") )
                return 0
                ;;
            config)
                COMPREPLY=( $(compgen -W "init show path" -- "$cur") )
                return 0
                ;;
        esac
    }
    complete -F _wt_complete wt
fi

# Zsh completion
if [ -n "$ZSH_VERSION" ]; then
    _wt_complete_zsh() {
        local -a commands branches
        commands=(
            'checkout:Checkout existing branch in new worktree'
            'co:Checkout existing branch in new worktree'
            'create:Create new branch in worktree'
            'pr:Checkout GitHub PR in worktree'
            'mr:Checkout GitLab MR in worktree'
            'list:List all worktrees'
            'ls:List all worktrees'
            'remove:Remove a worktree'
            'rm:Remove a worktree'
            'cleanup:Remove worktrees for merged branches'
            'prune:Remove worktree administrative files'
            'help:Show help'
            'shellenv:Output shell function for auto-cd'
            'init:Initialize shell integration'
            'info:Show worktree location configuration'
            'config:Manage wt configuration'
            'version:Show version information'
        )

        if (( CURRENT == 2 )); then
            _describe 'command' commands
        elif (( CURRENT == 3 )); then
            case "$words[2]" in
                checkout|co|remove|rm)
                    branches=(${(f)"$(git worktree list 2>/dev/null | tail -n +2 | sed -n 's/.*\[\([^]]*\)\].*/\1/p')"})
                    _describe 'branch' branches
                    ;;
                config)
                    local -a config_cmds
                    config_cmds=(
                        'init:Create a default configuration file'
                        'show:Show effective configuration with sources'
                        'path:Print the config file path'
                    )
                    _describe 'config command' config_cmds
                    ;;
            esac
        fi
    }
    # Only register completion if compdef is available
    if (( $+functions[compdef] )); then
        compdef _wt_complete_zsh wt
    fi
fi
`)
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("wt version %s\n", version)
	},
}

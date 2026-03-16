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
	version           = "dev"
	worktreeRoot      string
	worktreeStrategy  string
	worktreePattern   string
	worktreeSeparator string
)

func init() {
	loadWorktreeConfig()
	rootCmd.Long = buildRootCmdLong()
}

func main() {
	// Re-load config after cobra parses flags so --config is available
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := validateOutputFormat(); err != nil {
			return err
		}
		if configFlag != "" {
			loadWorktreeConfig()
			rootCmd.Long = buildRootCmdLong()
		}
		return nil
	}
	if err := rootCmd.Execute(); err != nil {
		if isJSONOutput() {
			_ = emitJSONError(rootCmd, err)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:           "wt",
	Short:         "Git worktree helper with organized directory structure",
	Long:          "",
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return printCommandHelp(cmd)
	},
}

func printCommandHelp(cmd *cobra.Command) error {
	return cmd.Help()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&configFlag, "config", "", "Path to config file (default: ~/.config/wt/config.toml)")
	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", formatText, "Output format: text or json")

	defaultHelp := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if !isJSONOutput() {
			defaultHelp(cmd, args)
			return
		}

		buf := bytes.NewBuffer(nil)
		origOut := cmd.OutOrStdout()
		origErr := cmd.ErrOrStderr()
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		defaultHelp(cmd, args)
		cmd.SetOut(origOut)
		cmd.SetErr(origErr)

		_ = emitJSONSuccess(cmd, map[string]any{"help": buf.String()})
	})

	rootCmd.AddCommand(checkoutCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(mrCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(pruneCmd)
	rootCmd.AddCommand(cleanupCmd)
	rootCmd.AddCommand(migrateCmd)
	rootCmd.AddCommand(shellenvCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(infoCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(examplesCmd)
	removeCmd.Flags().BoolVarP(&removeForce, "force", "f", false, "Force removal even if worktree has modifications")
	cleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "Preview what would be removed without making changes")
	cleanupCmd.Flags().BoolVarP(&cleanupForce, "force", "f", false, "Remove all merged worktrees without confirmation")
	migrateCmd.Flags().BoolVarP(&migrateForce, "force", "f", false, "Force migration when target path exists and is non-empty")
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
	rendered, err := renderWorktreePath(info, branch)
	if err != nil {
		return "", err
	}

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

func renderWorktreePath(info repoInfo, branch string) (string, error) {
	pattern, err := resolveWorktreePattern()
	if err != nil {
		return "", err
	}

	sep := worktreeSeparator

	// transformValue replaces "/" and "\" with the configured separator.
	transformValue := func(s string) string {
		return strings.ReplaceAll(strings.ReplaceAll(s, "/", sep), "\\", sep)
	}

	envMap := map[string]string{}
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = transformValue(parts[1])
		}
	}

	context := map[string]any{
		"repo": repoInfo{
			Main:  info.Main, // full path — NOT transformed
			Host:  info.Host,
			Owner: transformValue(info.Owner),
			Name:  info.Name,
		},
		"branch":       strings.TrimSpace(transformValue(branch)),
		"worktreeRoot": worktreeRoot, // full path — NOT transformed
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
		return "{.repo.Main}/../{.repo.Name}-{.branch}", nil
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
	if isJSONOutput() {
		return
	}
	fmt.Printf("wt navigating to: %s\n", path)
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

// Hooks

// getHooks returns the hook commands for a given hook name.
func getHooks(hookName string) []string {
	switch hookName {
	case "pre_create":
		return worktreeHooks.PreCreate
	case "post_create":
		return worktreeHooks.PostCreate
	case "pre_checkout":
		return worktreeHooks.PreCheckout
	case "post_checkout":
		return worktreeHooks.PostCheckout
	case "pre_remove":
		return worktreeHooks.PreRemove
	case "post_remove":
		return worktreeHooks.PostRemove
	case "pre_pr":
		return worktreeHooks.PrePR
	case "post_pr":
		return worktreeHooks.PostPR
	case "pre_mr":
		return worktreeHooks.PreMR
	case "post_mr":
		return worktreeHooks.PostMR
	default:
		return nil
	}
}

// buildHookEnv creates the environment variables map for hook commands.
func buildHookEnv(info repoInfo, branch, worktreePath string) map[string]string {
	return map[string]string{
		"WT_PATH":       worktreePath,
		"WT_BRANCH":     branch,
		"WT_MAIN":       info.Main,
		"WT_REPO_NAME":  info.Name,
		"WT_REPO_HOST":  info.Host,
		"WT_REPO_OWNER": info.Owner,
	}
}

// runHooks executes hook commands. For pre-hooks (hookName starts with "pre_"),
// any command failure aborts the operation. For post-hooks, failures are warned
// but do not fail the overall operation.
func runHooks(hookName string, hookCommands []string, env map[string]string) error {
	if os.Getenv("WT_HOOKS_DISABLED") == "1" {
		return nil
	}
	if len(hookCommands) == 0 {
		return nil
	}

	isPre := strings.HasPrefix(hookName, "pre_")

	// Build environment slice from current env + hook vars
	environ := os.Environ()
	for k, v := range env {
		environ = append(environ, fmt.Sprintf("%s=%s", k, v))
	}

	for _, cmdStr := range hookCommands {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/c", cmdStr)
		} else {
			cmd = exec.Command("sh", "-c", cmdStr)
		}
		cmd.Env = environ
		if isJSONOutput() {
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
		} else {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			if isPre {
				return fmt.Errorf("command %q failed: %w", cmdStr, err)
			}
			fmt.Fprintf(os.Stderr, "\u26a0 %s hook failed: command %q: %v\n", hookName, cmdStr, err)
		}
	}
	return nil
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
			if isJSONOutput() {
				return fmt.Errorf("wt checkout with --format json requires an explicit branch argument")
			}
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
			if isJSONOutput() {
				return emitJSONSuccess(cmd, map[string]any{
					"status":      "exists",
					"branch":      branch,
					"path":        existingPath,
					"navigate_to": existingPath,
				})
			}
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

		hookEnv := buildHookEnv(info, branch, path)

		// Run pre-checkout hooks
		if err := runHooks("pre_checkout", getHooks("pre_checkout"), hookEnv); err != nil {
			return fmt.Errorf("pre-checkout hook failed: %w", err)
		}

		// Create worktree
		gitCmd := exec.Command("git", "worktree", "add", path, branch)
		if !isJSONOutput() {
			gitCmd.Stdout = os.Stdout
			gitCmd.Stderr = os.Stderr
		}
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		// Run post-checkout hooks (warn only)
		_ = runHooks("post_checkout", getHooks("post_checkout"), hookEnv)

		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{
				"status":      "created",
				"branch":      branch,
				"path":        path,
				"navigate_to": path,
			})
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
			if isJSONOutput() {
				return emitJSONSuccess(cmd, map[string]any{
					"status":      "exists",
					"branch":      branch,
					"base":        base,
					"path":        existingPath,
					"navigate_to": existingPath,
				})
			}
			fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
			printCDMarker(existingPath)
			return nil
		}

		path, err := buildWorktreePath(info, branch)
		if err != nil {
			return err
		}

		hookEnv := buildHookEnv(info, branch, path)

		// Run pre-create hooks
		if err := runHooks("pre_create", getHooks("pre_create"), hookEnv); err != nil {
			return fmt.Errorf("pre-create hook failed: %w", err)
		}

		// Create new branch and worktree
		gitCmd := exec.Command("git", "worktree", "add", path, "-b", branch, base)
		if !isJSONOutput() {
			gitCmd.Stdout = os.Stdout
			gitCmd.Stderr = os.Stderr
		}
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to create worktree: %w", err)
		}

		// Run post-create hooks (warn only)
		_ = runHooks("post_create", getHooks("post_create"), hookEnv)

		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{
				"status":      "created",
				"branch":      branch,
				"base":        base,
				"path":        path,
				"navigate_to": path,
			})
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
			if isJSONOutput() {
				return fmt.Errorf("wt pr with --format json requires an explicit PR number or URL")
			}
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

		return checkoutPROrMR(cmd, input, RemoteGitHub)
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
			if isJSONOutput() {
				return fmt.Errorf("wt mr with --format json requires an explicit MR number or URL")
			}
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

		return checkoutPROrMR(cmd, input, RemoteGitLab)
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

func checkoutPROrMR(cmd *cobra.Command, input string, remoteType RemoteType) error {
	jsonMode := isJSONOutput()
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
		if jsonMode {
			return emitJSONSuccess(cmd, map[string]any{
				"status":      "exists",
				"id":          prNumber,
				"kind":        prefix,
				"branch":      branch,
				"path":        existingPath,
				"navigate_to": existingPath,
			})
		}
		fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
		printCDMarker(existingPath)
		return nil
	}

	path, err := buildWorktreePath(info, branch)
	if err != nil {
		return err
	}

	// Determine hook name based on remote type
	hookPrefix := "pr"
	if remoteType == RemoteGitLab {
		hookPrefix = "mr"
	}
	hookEnv := buildHookEnv(info, branch, path)

	// Run pre-pr/pre-mr hooks
	preHookName := "pre_" + hookPrefix
	if err := runHooks(preHookName, getHooks(preHookName), hookEnv); err != nil {
		return fmt.Errorf("%s hook failed: %w", preHookName, err)
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
	if !jsonMode {
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
	}
	if err := gitCmd.Run(); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	// Ensure upstream tracking is set (best-effort, may fail for fork PRs)
	upstreamCmd := exec.Command("git", "branch", "--set-upstream-to",
		fmt.Sprintf("origin/%s", branch), branch)
	_ = upstreamCmd.Run()

	// Run post-pr/post-mr hooks (warn only)
	postHookName := "post_" + hookPrefix
	_ = runHooks(postHookName, getHooks(postHookName), hookEnv)

	if jsonMode {
		return emitJSONSuccess(cmd, map[string]any{
			"status":      "created",
			"id":          prNumber,
			"kind":        prefix,
			"branch":      branch,
			"path":        path,
			"navigate_to": path,
		})
	}

	fmt.Printf("✓ %s #%s (%s) checked out at: %s\n", strings.ToUpper(prefix), prNumber, branch, path)

	printCDMarker(path)
	return nil
}

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List all worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		if isJSONOutput() {
			entries, err := getWorktreeListPorcelain()
			if err != nil {
				return err
			}
			return emitJSONSuccess(cmd, map[string]any{"worktrees": entries})
		}

		gitCmd := exec.Command("git", "worktree", "list")
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
		if err := gitCmd.Run(); err != nil {
			return err
		}
		return nil
	},
}

var (
	removeForce   bool
	cleanupDryRun bool
	cleanupForce  bool
	migrateForce  bool
)

var removeCmd = &cobra.Command{
	Use:     "remove [branch]",
	Aliases: []string{"rm"},
	Short:   "Remove a worktree",
	Args:    cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var branch string
		jsonMode := isJSONOutput()

		// Interactive selection if no branch provided
		if len(args) == 0 {
			if jsonMode {
				return fmt.Errorf("wt remove with --format json requires an explicit branch argument")
			}
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

		// Build hook env for remove hooks
		info, _ := getRepoInfo()
		hookEnv := buildHookEnv(info, branch, existingPath)

		// Run pre-remove hooks
		if err := runHooks("pre_remove", getHooks("pre_remove"), hookEnv); err != nil {
			return fmt.Errorf("pre-remove hook failed: %w", err)
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
		if !jsonMode {
			gitCmd.Stdout = os.Stdout
			gitCmd.Stderr = os.Stderr
		}
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to remove worktree: %w", err)
		}

		if err := cleanupWorktreePath(existingPath); err != nil {
			return err
		}

		// Run post-remove hooks (warn only)
		_ = runHooks("post_remove", getHooks("post_remove"), hookEnv)

		if jsonMode {
			return emitJSONSuccess(cmd, map[string]any{
				"status":      "removed",
				"branch":      branch,
				"path":        existingPath,
				"navigate_to": mainWorktreePath,
			})
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
		jsonMode := isJSONOutput()

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
			if jsonMode {
				return emitJSONSuccess(cmd, map[string]any{"removed": 0, "skipped": 0, "base": base, "worktrees": []string{}})
			}
			fmt.Println("No worktrees found for merged branches")
			return nil
		}

		if jsonMode && !cleanupDryRun && !cleanupForce {
			return fmt.Errorf("wt cleanup with --format json requires --force or --dry-run")
		}

		// Dry run mode - just show what would be removed
		if cleanupDryRun {
			if jsonMode {
				planned := make([]map[string]string, 0, len(toRemove))
				for _, branch := range toRemove {
					if path, exists := worktreeExists(branch); exists {
						planned = append(planned, map[string]string{"branch": branch, "path": path})
					}
				}
				return emitJSONSuccess(cmd, map[string]any{"dry_run": true, "base": base, "worktrees": planned})
			}
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
			if !jsonMode {
				gitCmd.Stdout = os.Stdout
				gitCmd.Stderr = os.Stderr
			}
			if err := gitCmd.Run(); err != nil {
				if jsonMode {
					skipped++
					continue
				}
				fmt.Printf("  Failed to remove %s: %v\n", branch, err)
				continue
			}

			if err := cleanupWorktreePath(existingPath); err != nil {
				if jsonMode {
					continue
				}
				fmt.Printf("  Warning: failed to cleanup path for %s: %v\n", branch, err)
			}

			if !jsonMode {
				fmt.Printf("✓ Removed worktree: %s\n", branch)
			}
			removed++
		}

		// Run prune at the end
		pruneGitCmd := exec.Command("git", "worktree", "prune")
		_ = pruneGitCmd.Run()

		if jsonMode {
			return emitJSONSuccess(cmd, map[string]any{"dry_run": false, "base": base, "removed": removed, "skipped": skipped})
		}

		fmt.Printf("\nCleanup complete: %d removed, %d skipped\n", removed, skipped)
		return nil
	},
}

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove worktree administrative files",
	RunE: func(cmd *cobra.Command, args []string) error {
		gitCmd := exec.Command("git", "worktree", "prune")
		if !isJSONOutput() {
			gitCmd.Stdout = os.Stdout
			gitCmd.Stderr = os.Stderr
		}
		if err := gitCmd.Run(); err != nil {
			return err
		}

		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{"status": "pruned"})
		}

		fmt.Println("✓ Pruned stale worktree administrative files")
		return nil
	},
}

var infoCmd = &cobra.Command{
	Use:   "info",
	Short: "Show worktree location configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonMode := isJSONOutput()
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

		hooks := map[string][]string{
			"pre_create":    worktreeHooks.PreCreate,
			"post_create":   worktreeHooks.PostCreate,
			"pre_checkout":  worktreeHooks.PreCheckout,
			"post_checkout": worktreeHooks.PostCheckout,
			"pre_remove":    worktreeHooks.PreRemove,
			"post_remove":   worktreeHooks.PostRemove,
			"pre_pr":        worktreeHooks.PrePR,
			"post_pr":       worktreeHooks.PostPR,
			"pre_mr":        worktreeHooks.PreMR,
			"post_mr":       worktreeHooks.PostMR,
		}

		if jsonMode {
			return emitJSONSuccess(cmd, map[string]any{
				"config": map[string]string{
					"path":      configFilePath,
					"status":    configStatus,
					"strategy":  worktreeStrategy,
					"pattern":   pattern,
					"root":      worktreeRoot,
					"separator": worktreeSeparator,
				},
				"strategies": []map[string]string{
					{"name": "global", "pattern": "{.worktreeRoot}/{.repo.Name}/{.branch}"},
					{"name": "sibling-repo", "pattern": "{.repo.Main}/../{.repo.Name}-{.branch}"},
					{"name": "parent-branches", "pattern": "{.repo.Main}/../{.branch}"},
					{"name": "parent-worktrees", "pattern": "{.repo.Main}/../{.repo.Name}.worktrees/{.branch}"},
					{"name": "parent-dotdir", "pattern": "{.repo.Main}/../.worktrees/{.branch}"},
					{"name": "inside-dotdir", "pattern": "{.repo.Main}/.worktrees/{.branch}"},
					{"name": "custom", "pattern": "requires pattern setting"},
				},
				"pattern_variables": []string{"{.repo.Name}", "{.repo.Main}", "{.repo.Owner}", "{.repo.Host}", "{.branch}", "{.worktreeRoot}", "{.env.VARNAME}"},
				"hooks":             hooks,
			})
		}

		fmt.Printf(`Config:    %s (%s)

Strategy:  %s
Pattern:   %s
Root:      %s
Separator: %q

Strategies:
  global           -> {.worktreeRoot}/{.repo.Name}/{.branch}
  sibling-repo     -> {.repo.Main}/../{.repo.Name}-{.branch}
  parent-branches  -> {.repo.Main}/../{.branch}
  parent-worktrees -> {.repo.Main}/../{.repo.Name}.worktrees/{.branch}
  parent-dotdir    -> {.repo.Main}/../.worktrees/{.branch}
  inside-dotdir    -> {.repo.Main}/.worktrees/{.branch}
  custom           -> requires pattern setting

Pattern variables: {.repo.Name}, {.repo.Main}, {.repo.Owner}, {.repo.Host}, {.branch}, {.worktreeRoot}, {.env.VARNAME}
Note: The separator setting controls how "/" and "\" in value variables are replaced.
      Default "/" preserves slashes (nested dirs). Set to "-" or "_" for flat paths.
      Path variables ({.repo.Main}, {.worktreeRoot}) are never transformed.
Note: {.env.VARNAME} accesses the environment variable VARNAME (e.g. {.env.HOME}).
`, configFilePath, configStatus, worktreeStrategy, pattern, worktreeRoot, worktreeSeparator)

		// Show configured hooks
		hookNames := []struct {
			name  string
			hooks []string
		}{
			{"pre_create", worktreeHooks.PreCreate},
			{"post_create", worktreeHooks.PostCreate},
			{"pre_checkout", worktreeHooks.PreCheckout},
			{"post_checkout", worktreeHooks.PostCheckout},
			{"pre_remove", worktreeHooks.PreRemove},
			{"post_remove", worktreeHooks.PostRemove},
			{"pre_pr", worktreeHooks.PrePR},
			{"post_pr", worktreeHooks.PostPR},
			{"pre_mr", worktreeHooks.PreMR},
			{"post_mr", worktreeHooks.PostMR},
		}
		hasHooks := false
		for _, h := range hookNames {
			if len(h.hooks) > 0 {
				hasHooks = true
				break
			}
		}
		if hasHooks {
			fmt.Println("Hooks:")
			for _, h := range hookNames {
				if len(h.hooks) > 0 {
					for _, cmd := range h.hooks {
						fmt.Printf("  %-15s %s\n", h.name+":", cmd)
					}
				}
			}
			fmt.Println()
		} else {
			fmt.Println("Hooks:    (none configured)")
			fmt.Println()
		}

		return nil
	},
}

var configInitForce bool

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage wt configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printCommandHelp(cmd)
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
		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]string{"path": path, "status": "created"})
		}
		fmt.Printf("Created config file: %s\n", path)
		return nil
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show effective configuration with sources",
	RunE: func(cmd *cobra.Command, args []string) error {
		pattern := configShowPatternValue()

		configStatus := "not found"
		if configFileFound {
			configStatus = "found"
		}

		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]any{
				"config_file": map[string]string{
					"path":   configFilePath,
					"status": configStatus,
				},
				"effective": map[string]any{
					"root":      map[string]string{"value": worktreeRoot, "source": configSources.Root},
					"strategy":  map[string]string{"value": worktreeStrategy, "source": configSources.Strategy},
					"pattern":   map[string]string{"value": pattern, "source": configSources.Pattern},
					"separator": map[string]string{"value": worktreeSeparator, "source": configSources.Separator},
				},
			})
		}

		fmt.Printf("Config file: %s (%s)\n\n", configFilePath, configStatus)
		fmt.Printf("Effective configuration:\n")
		fmt.Printf("  %-10s = %-40s (%s)\n", "root", worktreeRoot, configSources.Root)
		fmt.Printf("  %-10s = %-40s (%s)\n", "strategy", worktreeStrategy, configSources.Strategy)
		fmt.Printf("  %-10s = %-40s (%s)\n", "pattern", pattern, configSources.Pattern)
		fmt.Printf("  %-10s = %-40s (%s)\n", "separator", fmt.Sprintf("%q", worktreeSeparator), configSources.Separator)
		return nil
	},
}

func configShowPatternValue() string {
	pattern, err := resolveWorktreePattern()
	if err == nil {
		return pattern
	}

	return "(none)"
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	RunE: func(cmd *cobra.Command, args []string) error {
		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]string{"path": resolveConfigPath(configFlag)})
		}
		fmt.Println(resolveConfigPath(configFlag))
		return nil
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
		if isJSONOutput() {
			_ = emitJSONSuccess(cmd, map[string]string{
				"note": "shellenv outputs shell script text; run without --format json to source it",
			})
			return
		}
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

    # In JSON mode, keep stdout machine-readable and skip auto-navigation.
    $isJson = $false
    for ($i = 0; $i -lt $args.Count; $i++) {
        if ($args[$i] -eq '--format' -and $i + 1 -lt $args.Count -and $args[$i + 1] -eq 'json') {
            $isJson = $true
        }
        if ($args[$i] -eq '--format=json') {
            $isJson = $true
        }
    }
    if ($isJson) {
        $global:LASTEXITCODE = $exitCode
        return
    }

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

    $commands = @('checkout', 'co', 'create', 'pr', 'mr', 'list', 'ls', 'remove', 'rm', 'cleanup', 'migrate', 'prune', 'help', 'shellenv', 'init', 'info', 'config', 'examples', 'version')

    # Get the position in the command line
    $position = $commandAst.CommandElements.Count - 1

    if ($position -eq 0) {
        # Complete commands
        $commands | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
            [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
        }
    } elseif ($position -eq 1) {
        $subCommand = $commandAst.CommandElements[1].Value
        if ($subCommand -in @('checkout', 'co', 'create')) {
            # Complete branch names from all local and remote branches
            $remotes = (git remote 2>$null) -join '|'
            $branches = git branch -a --format='%(refname:short)' 2>$null | Where-Object { $_ -notmatch 'HEAD' } | ForEach-Object { $_ -replace "^($remotes)/", '' } | Sort-Object -Unique
            $branches | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
                [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
            }
        } elseif ($subCommand -in @('remove', 'rm')) {
            # Complete branch names from existing worktrees
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
    # Avoid wrapping shellenv generation itself through script(1)
    # to prevent control characters in process substitution output.
    if [ "$1" = "shellenv" ]; then
        command wt "$@"
        return $?
    fi

    # In JSON mode, keep stdout machine-readable and skip auto-navigation.
    case " $* " in
        *" --format json "*|*" --format=json "*)
            command wt "$@"
            return $?
            ;;
    esac

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
        commands="checkout co create pr mr list ls remove rm cleanup migrate prune help shellenv init info config examples version"

        # Complete commands if first argument
        if [ $COMP_CWORD -eq 1 ]; then
            COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
            return 0
        fi

        # Complete branch names for checkout/co/create and worktree branches for remove/rm
        case "$prev" in
            checkout|co|create)
                local branches remotes
                remotes=$(git remote 2>/dev/null | paste -sd'|' -)
                branches=$(git branch -a --format='%(refname:short)' 2>/dev/null | grep -v 'HEAD' | sed -E "s#^($remotes)/##" | sort -u)
                COMPREPLY=( $(compgen -W "$branches" -- "$cur") )
                return 0
                ;;
            remove|rm)
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
            'migrate:Migrate existing worktrees to configured paths'
            'prune:Remove worktree administrative files'
            'help:Show help'
            'shellenv:Output shell function for auto-cd'
            'init:Initialize shell integration'
            'info:Show worktree location configuration'
            'config:Manage wt configuration'
            'examples:Show practical command examples'
            'version:Show version information'
        )

        if (( CURRENT == 2 )); then
            _describe 'command' commands
        elif (( CURRENT == 3 )); then
            case "$words[2]" in
                checkout|co|create)
                    local remotes
                    remotes=$(git remote 2>/dev/null | paste -sd'|' -)
                    branches=(${(f)"$(git branch -a --format='%(refname:short)' 2>/dev/null | grep -v 'HEAD' | sed -E "s#^($remotes)/##" | sort -u)"})
                    _describe 'branch' branches
                    ;;
                remove|rm)
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
	RunE: func(cmd *cobra.Command, args []string) error {
		if isJSONOutput() {
			return emitJSONSuccess(cmd, map[string]string{"version": version})
		}
		fmt.Printf("wt version %s\n", version)
		return nil
	},
}

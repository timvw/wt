package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

var (
	version      = "dev"
	worktreeRoot string
)

func init() {
	// Set worktree root from environment or default
	worktreeRoot = os.Getenv("WORKTREE_ROOT")
	if worktreeRoot == "" {
		home, _ := os.UserHomeDir()
		worktreeRoot = filepath.Join(home, "dev", "worktrees")
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "wt",
	Short: "Git worktree helper with organized directory structure",
	Long: `Git-like worktree management with organized directory structure.

Worktrees are organized at: ` + worktreeRoot + `/<repo>/<branch>
Set WORKTREE_ROOT to customize the location.`,
	Run: func(cmd *cobra.Command, args []string) {
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(checkoutCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(prCmd)
	rootCmd.AddCommand(mrCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(pruneCmd)
	rootCmd.AddCommand(shellenvCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	removeCmd.Flags().BoolVarP(&removeForce, "force", "f", false, "Force removal even if worktree has modifications")
	initCmd.Flags().BoolVar(&initDryRun, "dry-run", false, "Preview changes without modifying files")
	initCmd.Flags().BoolVar(&initUninstall, "uninstall", false, "Remove wt configuration from shell")
	initCmd.Flags().BoolVar(&initNoPrompt, "no-prompt", false, "Skip activation instructions (for automated installs)")
}

// Helper functions

func getRepoName() (string, error) {
	// Try to get from remote origin URL
	cmd := exec.Command("git", "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err == nil {
		url := strings.TrimSpace(string(output))
		base := filepath.Base(url)
		return strings.TrimSuffix(base, ".git"), nil
	}

	// Fallback to toplevel directory name
	cmd = exec.Command("git", "rev-parse", "--show-toplevel")
	output, err = cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not in a git repository")
	}
	toplevel := strings.TrimSpace(string(output))
	return filepath.Base(toplevel), nil
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

func ensureWorktreePath(repo, branch string) (string, error) {
	targetRoot := filepath.Join(worktreeRoot, repo)

	info, err := os.Stat(targetRoot)
	switch {
	case err == nil:
		if !info.IsDir() {
			return "", fmt.Errorf("WORKTREE_ROOT path %s is not a directory", targetRoot)
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(targetRoot, 0o755); err != nil {
			return "", fmt.Errorf("failed to create WORKTREE_ROOT directory %s: %w", targetRoot, err)
		}
	default:
		return "", fmt.Errorf("failed to access WORKTREE_ROOT directory %s: %w", targetRoot, err)
	}

	return filepath.Join(targetRoot, branch), nil
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
		repo, err := getRepoName()
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

		path, err := ensureWorktreePath(repo, branch)
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

		repo, err := getRepoName()
		if err != nil {
			return err
		}

		// Check if worktree already exists
		if existingPath, exists := worktreeExists(branch); exists {
			fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
			printCDMarker(existingPath)
			return nil
		}

		path, err := ensureWorktreePath(repo, branch)
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

Uses the 'gh' CLI to fetch and checkout pull requests.
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

Uses the 'glab' CLI to fetch and checkout merge requests.
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

	repo, err := getRepoName()
	if err != nil {
		return err
	}

	branch := fmt.Sprintf("%s-%s", prefix, prNumber)

	// Check if worktree already exists
	if existingPath, exists := worktreeExists(branch); exists {
		fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
		printCDMarker(existingPath)
		return nil
	}

	path, err := ensureWorktreePath(repo, branch)
	if err != nil {
		return err
	}

	// Fetch the PR/MR
	fetchCmd := exec.Command("git", "fetch", "origin", fmt.Sprintf("%s:%s", refSpec, branch))
	fetchCmd.Stderr = os.Stderr
	_ = fetchCmd.Run() // Ignore errors, branch might already exist

	// Create worktree
	gitCmd := exec.Command("git", "worktree", "add", path, branch)
	gitCmd.Stdout = os.Stdout
	gitCmd.Stderr = os.Stderr
	if err := gitCmd.Run(); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}

	fmt.Printf("✓ %s #%s checked out at: %s\n", strings.ToUpper(prefix), prNumber, path)
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

var removeForce bool

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

    $commands = @('checkout', 'co', 'create', 'pr', 'mr', 'list', 'ls', 'remove', 'rm', 'prune', 'help', 'shellenv', 'init', 'version')

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
        commands="checkout co create pr mr list ls remove rm prune help shellenv init version"

        # Complete commands if first argument
        if [ $COMP_CWORD -eq 1 ]; then
            COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
            return 0
        fi

        # Complete branch names for checkout/remove/rm
        case "$prev" in
            checkout|co|remove|rm)
                local branches
                branches=$(git worktree list 2>/dev/null | awk 'NR>1 {match($0, /\[([^]]+)\]/, arr); if (arr[1]) print arr[1]}')
                COMPREPLY=( $(compgen -W "$branches" -- "$cur") )
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
            'prune:Remove worktree administrative files'
            'help:Show help'
            'shellenv:Output shell function for auto-cd'
            'init:Initialize shell integration'
            'version:Show version information'
        )

        if (( CURRENT == 2 )); then
            _describe 'command' commands
        elif (( CURRENT == 3 )); then
            case "$words[2]" in
                checkout|co|remove|rm)
                    branches=(${(f)"$(git worktree list 2>/dev/null | awk 'NR>1 {match($0, /\[([^]]+)\]/, arr); if (arr[1]) print arr[1]}')"})
                    _describe 'branch' branches
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

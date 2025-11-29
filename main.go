package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

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
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(removeCmd)
	rootCmd.AddCommand(pruneCmd)
	rootCmd.AddCommand(shellenvCmd)
	rootCmd.AddCommand(versionCmd)
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

func getRemoteType() RemoteType {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return RemoteUnknown
	}

	url := strings.TrimSpace(string(output))
	if strings.Contains(url, "github.com") {
		return RemoteGitHub
	}
	if strings.Contains(url, "gitlab.com") || strings.Contains(url, "gitlab") {
		return RemoteGitLab
	}

	return RemoteUnknown
}

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

func printCDMarker(path string) {
	fmt.Printf("TREE_ME_CD:%s\n", path)
}

// Commands

var checkoutCmd = &cobra.Command{
	Use:     "checkout <branch>",
	Aliases: []string{"co"},
	Short:   "Checkout existing branch in new worktree",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branch := args[0]
		repo, err := getRepoName()
		if err != nil {
			return err
		}

		path := filepath.Join(worktreeRoot, repo, branch)

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

		path := filepath.Join(worktreeRoot, repo, branch)

		// Check if worktree already exists
		if existingPath, exists := worktreeExists(branch); exists {
			fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
			printCDMarker(existingPath)
			return nil
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
	Use:     "pr <number|url>",
	Aliases: []string{"mr"},
	Short:   "Checkout PR/MR in worktree (uses gh for GitHub, glab for GitLab)",
	Long: `Checkout a Pull Request (GitHub) or Merge Request (GitLab) in a worktree.

Automatically detects whether you're using GitHub or GitLab based on
the git remote URL and uses the appropriate CLI tool (gh or glab).

Examples:
  wt pr 123                                    # PR/MR number
  wt pr https://github.com/org/repo/pull/123   # GitHub PR URL
  wt pr https://gitlab.com/org/repo/-/merge_requests/123  # GitLab MR URL
  wt mr 123                                    # Same as 'wt pr 123'`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		input := args[0]
		prNumber, err := getPRNumber(input)
		if err != nil {
			return err
		}

		// Detect remote type
		remoteType := getRemoteType()
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
			return fmt.Errorf("unable to detect remote type (GitHub or GitLab)")
		}

		repo, err := getRepoName()
		if err != nil {
			return err
		}

		branch := fmt.Sprintf("%s-%s", prefix, prNumber)
		path := filepath.Join(worktreeRoot, repo, branch)

		// Check if worktree already exists
		if existingPath, exists := worktreeExists(branch); exists {
			fmt.Printf("✓ Worktree already exists: %s\n", existingPath)
			printCDMarker(existingPath)
			return nil
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
	},
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

var removeCmd = &cobra.Command{
	Use:     "remove <branch>",
	Aliases: []string{"rm"},
	Short:   "Remove a worktree",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		branch := args[0]

		existingPath, exists := worktreeExists(branch)
		if !exists {
			return fmt.Errorf("no worktree found for branch: %s", branch)
		}

		gitCmd := exec.Command("git", "worktree", "remove", existingPath)
		gitCmd.Stdout = os.Stdout
		gitCmd.Stderr = os.Stderr
		if err := gitCmd.Run(); err != nil {
			return fmt.Errorf("failed to remove worktree: %w", err)
		}

		fmt.Printf("✓ Removed worktree: %s\n", existingPath)
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

Add this to your ~/.bashrc or ~/.zshrc:
  source <(wt shellenv)

This enables:
- Automatic cd to worktree after checkout/create/pr commands
- Tab completion for commands and branch names`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Print(`wt() {
    local output
    output=$(command wt "$@")
    local exit_code=$?
    echo "$output"
    if [ $exit_code -eq 0 ]; then
        local cd_path=$(echo "$output" | grep "^TREE_ME_CD:" | cut -d: -f2-)
        [ -n "$cd_path" ] && cd "$cd_path"
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
        commands="checkout co create pr mr list ls remove rm prune help shellenv"

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
            'pr:Checkout PR/MR in worktree'
            'mr:Checkout PR/MR in worktree'
            'list:List all worktrees'
            'ls:List all worktrees'
            'remove:Remove a worktree'
            'rm:Remove a worktree'
            'prune:Remove worktree administrative files'
            'help:Show help'
            'shellenv:Output shell function for auto-cd'
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
    compdef _wt_complete_zsh wt
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

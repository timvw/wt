package cmd

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var shellenvCmd = &cobra.Command{
	Use:   "shellenv [shell]",
	Short: "Output shell function for auto-cd (source this)",
	Long: `Output shell integration code for automatic directory navigation.

Add this to the END of your ~/.bashrc or ~/.zshrc:
  source <(wt shellenv)

For fish, add this to your ~/.config/fish/config.fish:
  wt shellenv fish | source

For PowerShell, add this to your $PROFILE:
  Invoke-Expression (& wt shellenv)

Note: For zsh, place this AFTER compinit to enable tab completion.

An optional shell argument (bash, zsh, fish, powershell/pwsh) can be given to
override auto-detection, e.g. 'wt shellenv fish'.

This enables:
- Automatic cd to worktree after checkout/create/pr/mr commands
- Tab completion for commands and branch names`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if isJSONOutput() {
			_ = emitJSONSuccess(cmd, map[string]string{
				"note": "shellenv outputs shell script text; run without --format json to source it",
			})
			return
		}
		// Determine which shell's integration to output. An explicit argument
		// takes priority, then $SHELL detection, then GOOS (Windows -> PowerShell).
		switch shellenvTargetShell(args) {
		case "fish":
			_, _ = os.Stdout.WriteString(fishShellenvScript())
			return
		case "powershell":
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

    $commands = @('checkout', 'co', 'create', 'default', 'pr', 'mr', 'list', 'ls', 'remove', 'rm', 'status', 'cleanup', 'migrate', 'prune', 'help', 'shellenv', 'init', 'info', 'config', 'examples', 'version')

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
		default:
			// Bash/Zsh integration for Unix systems
			writeBashZshShellenv()
		}
	},
}

func writeBashZshShellenv() {
	_, _ = os.Stdout.WriteString(`wt() {
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
        # Linux: script -q -c "..." file — must pass command as single string,
        # so we shell-quote each argument to preserve spaces and special chars.
        local quoted_args=""
        for arg in "$@"; do
            quoted_args="$quoted_args $(printf '%q' "$arg")"
        done
        script -q -c "command wt$quoted_args" "$log_file"
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
        commands="checkout co create default pr mr list ls remove rm status cleanup migrate prune help shellenv init info config examples version"

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
            'default:Navigate to the main worktree'
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
            'status:Show status dashboard of all worktrees'
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
}

// shellenvTargetShell determines which shell's integration script shellenv
// should output. Priority: explicit argument > GOOS (Windows -> PowerShell)
// > $SHELL detection.
// Note: bash and zsh share the same output, so both map to "bash" here.
func shellenvTargetShell(args []string) string {
	if len(args) > 0 {
		shell := strings.ToLower(args[0])
		switch shell {
		case "fish":
			return "fish"
		case "powershell", "pwsh":
			return "powershell"
		case "bash", "zsh":
			return "bash"
		}
	}

	if runtime.GOOS == "windows" {
		return "powershell"
	}

	if strings.Contains(os.Getenv("SHELL"), "fish") {
		return "fish"
	}

	return "bash"
}

// fishShellenvScript returns the fish shell integration script.
func fishShellenvScript() string {
	return `function wt
    # Avoid wrapping shellenv generation itself through script(1)
    # to prevent control characters in process substitution output.
    if test "$argv[1]" = "shellenv"
        command wt $argv
        return $status
    end

    # In JSON mode, keep stdout machine-readable and skip auto-navigation.
    if contains -- --format=json $argv
        command wt $argv
        return $status
    end
    set -l argv_str " $argv "
    if string match -q "* --format json *" $argv_str
        command wt $argv
        return $status
    end

    # Use script(1) to provide a PTY for interactive commands (e.g., promptui menus)
    # Command substitution (command wt) doesn't allocate a TTY, which breaks interactive prompts
    set -l log_file (mktemp -t wt.XXXXXX)

    # Detect OS to use correct script syntax (macOS vs Linux)
    if test (uname) = "Darwin"
        # macOS: script -q file command args
        script -q $log_file /bin/sh -c 'command wt "$@"' wt $argv
    else
        # Linux: script -q -c "..." file — the command must be a single string,
        # which script parses with $SHELL. That may be bash/zsh even when fish is
        # the interactive shell, and fish's own escaping (e.g. \t, \xHH) would be
        # misread there. Wrap each argument in POSIX single quotes instead, which
        # every POSIX shell and fish parse identically, preserving spaces and
        # special characters regardless of which shell runs the command.
        set -l quoted_args
        for arg in $argv
            set -l esc (string replace -a -- "'" "'\\''" $arg)
            set -a quoted_args "'$esc'"
        end
        script -q -c "command wt $quoted_args" $log_file
    end
    set -l exit_code $status

    # Extract the navigation marker for auto-cd
    set -l cd_path (grep '^wt navigating to: ' $log_file | tail -1 | sed 's/^wt navigating to: //')
    rm -f $log_file
    set cd_path (string trim -c \r -- $cd_path)

    if test $exit_code -eq 0 -a -n "$cd_path"
        cd "$cd_path"
    end
    return $exit_code
end

# Fish completion
complete -c wt -f
complete -c wt -n "__fish_use_subcommand" -a "checkout" -d "Checkout existing branch in new worktree"
complete -c wt -n "__fish_use_subcommand" -a "co" -d "Checkout existing branch in new worktree"
complete -c wt -n "__fish_use_subcommand" -a "create" -d "Create new branch in worktree"
complete -c wt -n "__fish_use_subcommand" -a "default" -d "Navigate to the main worktree"
complete -c wt -n "__fish_use_subcommand" -a "pr" -d "Checkout GitHub PR in worktree"
complete -c wt -n "__fish_use_subcommand" -a "mr" -d "Checkout GitLab MR in worktree"
complete -c wt -n "__fish_use_subcommand" -a "list" -d "List all worktrees"
complete -c wt -n "__fish_use_subcommand" -a "ls" -d "List all worktrees"
complete -c wt -n "__fish_use_subcommand" -a "remove" -d "Remove a worktree"
complete -c wt -n "__fish_use_subcommand" -a "rm" -d "Remove a worktree"
complete -c wt -n "__fish_use_subcommand" -a "status" -d "Show status dashboard of all worktrees"
complete -c wt -n "__fish_use_subcommand" -a "cleanup" -d "Remove worktrees for merged branches"
complete -c wt -n "__fish_use_subcommand" -a "migrate" -d "Migrate existing worktrees to configured paths"
complete -c wt -n "__fish_use_subcommand" -a "prune" -d "Remove worktree administrative files"
complete -c wt -n "__fish_use_subcommand" -a "help" -d "Show help"
complete -c wt -n "__fish_use_subcommand" -a "shellenv" -d "Output shell function for auto-cd"
complete -c wt -n "__fish_use_subcommand" -a "init" -d "Initialize shell integration"
complete -c wt -n "__fish_use_subcommand" -a "info" -d "Show worktree location configuration"
complete -c wt -n "__fish_use_subcommand" -a "config" -d "Manage wt configuration"
complete -c wt -n "__fish_use_subcommand" -a "examples" -d "Show practical command examples"
complete -c wt -n "__fish_use_subcommand" -a "version" -d "Show version information"

function __wt_complete_branches
    set -l remotes (git remote 2>/dev/null | string join '|')
    git branch -a --format='%(refname:short)' 2>/dev/null | grep -v 'HEAD' | sed -E "s#^($remotes)/##" | sort -u
end

function __wt_complete_worktree_branches
    git worktree list 2>/dev/null | tail -n +2 | sed -n 's/.*\[\([^]]*\)\].*/\1/p'
end

complete -c wt -n "__fish_seen_subcommand_from checkout co create" -a "(__wt_complete_branches)"
complete -c wt -n "__fish_seen_subcommand_from remove rm" -a "(__wt_complete_worktree_branches)"
complete -c wt -n "__fish_seen_subcommand_from config" -a "init show path"
`
}

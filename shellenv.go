package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

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
	},
}

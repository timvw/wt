# wt - Git Worktree Manager

[![CI](https://github.com/timvw/wt/actions/workflows/ci.yml/badge.svg)](https://github.com/timvw/wt/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/timvw/wt)](https://goreportcard.com/report/github.com/timvw/wt)
[![codecov](https://codecov.io/gh/timvw/wt/branch/main/graph/badge.svg)](https://codecov.io/gh/timvw/wt)
[![Go Reference](https://pkg.go.dev/badge/github.com/timvw/wt.svg)](https://pkg.go.dev/github.com/timvw/wt)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Release](https://img.shields.io/github/v/release/timvw/wt)](https://github.com/timvw/wt/releases)

A fast, simple Git worktree helper written in Go.
Inspired by [haacked/dotfiles/tree-me](https://github.com/haacked/dotfiles/blob/main/bin/tree-me).

## Features

- Organized worktree structure: `~/dev/worktrees/<repo>/<branch>`
- Simple commands for common worktree operations
- **Interactive selection menus** for checkout, remove, pr, and mr commands
- GitHub PR support via `wt pr` command (uses `gh` CLI)
- GitLab MR support via `wt mr` command (uses `glab` CLI)
- Shell integration with auto-cd functionality
- Tab completion for Bash and Zsh

## Installation

### Homebrew (macOS and Linux)

```bash
brew tap timvw/tap
brew install wt
```

### From Source

```bash
go install github.com/timvw/wt@latest
```

Or clone and build:

```bash
git clone https://github.com/timvw/wt.git
cd wt

# Using just (recommended)
just build            # builds to bin/wt
just install          # installs to /usr/local/bin (requires sudo)
just install-user     # installs to ~/bin (no sudo)

# Or build directly with go
mkdir -p bin
go build -o bin/wt .
sudo cp bin/wt /usr/local/bin/
```

### Shell Integration (Optional but Recommended)

Add this to the **END** of your `~/.bashrc` or `~/.zshrc`:

```bash
source <(wt shellenv)
```

**Note for zsh users:** Place this after `compinit` in your config file.

This enables:
- Automatic `cd` to worktree after `checkout`/`create`/`pr`/`mr` commands
- Tab completion for commands and branch names

## Usage

### Commands

```bash
# Checkout existing branch in new worktree
wt checkout feature-branch
wt co feature-branch              # short alias
wt co                             # interactive: select from available branches

# Create new branch in worktree (defaults to main/master as base)
wt create my-feature
wt create my-feature develop      # specify base branch

# Checkout GitHub PR in worktree (requires gh CLI)
wt pr 123                                          # GitHub PR number
wt pr https://github.com/org/repo/pull/123         # GitHub PR URL
wt pr                                              # interactive: select from open PRs

# Checkout GitLab MR in worktree (requires glab CLI)
wt mr 123                                          # GitLab MR number
wt mr https://gitlab.com/org/repo/-/merge_requests/123  # GitLab MR URL
wt mr                                              # interactive: select from open MRs

# List all worktrees
wt list
wt ls                             # short alias

# Remove a worktree
wt remove old-branch
wt rm old-branch                  # short alias
wt rm                             # interactive: select from existing worktrees

# Clean up stale worktree administrative files
wt prune

# Show shell integration code
wt shellenv

# Show version
wt version

# Show help
wt --help
wt <command> --help
```

### Interactive Selection

When you run `wt co`, `wt rm`, `wt pr`, or `wt mr` without arguments, you'll get an interactive selection menu:

```bash
# Interactive branch checkout
$ wt co
Use the arrow keys to navigate: ↓ ↑ → ←
? Select branch to checkout:
  ▸ feature/add-auth
    feature/update-docs
    bugfix/login-issue
    main

# Interactive worktree removal
$ wt rm
Use the arrow keys to navigate: ↓ ↑ → ←
? Select worktree to remove:
  ▸ feature/add-auth
    feature/update-docs
    bugfix/login-issue

# Interactive PR checkout (requires gh CLI)
$ wt pr
Use the arrow keys to navigate: ↓ ↑ → ←
? Select PR to checkout:
  ▸ #123: Add authentication feature
    #124: Update documentation
    #125: Fix login bug

# Interactive MR checkout (requires glab CLI)
$ wt mr
Use the arrow keys to navigate: ↓ ↑ → ←
? Select MR to checkout:
  ▸ !456: Add authentication feature
    !457: Update documentation
    !458: Fix login bug
```

### Examples

```bash
# Create a new feature branch from main
wt create add-auth-feature

# Checkout an existing branch
wt checkout bugfix-login

# Work on a GitHub PR
wt pr 456

# Work on a GitLab MR
wt mr 789

# List all your worktrees
wt list

# Remove a worktree when done
wt rm add-auth-feature
```

## Configuration

### Worktree Location

By default, worktrees are created at `~/dev/worktrees/<repo>/<branch>`.

Customize the location by setting the `WORKTREE_ROOT` environment variable:

```bash
export WORKTREE_ROOT="$HOME/projects/worktrees"
```

Add this to your `~/.bashrc` or `~/.zshrc` to make it permanent.

## Development

The project includes a `justfile` for common build tasks. Install [just](https://github.com/casey/just) to use it.

Available tasks:
```bash
just           # Show available recipes
just build     # Build the binary
just test      # Run tests
just clean     # Clean build artifacts
just build-all # Cross-compile for multiple platforms
```

## Requirements

- Git (obviously)
- `gh` CLI (optional, only needed for `wt pr` command to checkout GitHub PRs)
- `glab` CLI (optional, only needed for `wt mr` command to checkout GitLab MRs)

### For Building from Source

- Go 1.24+ (we support and test the latest two Go releases: 1.24 and 1.25)
- `just` (optional, for using the justfile)

## How It Works

The tool wraps Git's native worktree commands with a convenient interface and organized directory structure:

1. **Organized Structure**: All worktrees for a repo are kept together
2. **Smart Defaults**: Automatically detects repo name and default branch
3. **Prevents Duplicates**: Checks if a worktree already exists before creating
4. **Auto-CD**: With shell integration, automatically changes to the worktree directory
5. **Tab Completion**: Makes it easy to work with existing branches

## Comparison with Original

This Go port maintains feature parity with the original bash script while offering:

- Faster execution (compiled binary)
- No bash dependency
- Easier to distribute (single binary)
- Cross-platform support (builds on Windows, macOS, Linux)
- Built-in completion support via cobra

## License

MIT

## Credits

Based on [tree-me](https://github.com/haacked/dotfiles/blob/main/bin/tree-me) by Phil Haack.

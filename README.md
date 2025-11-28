# wt - Git Worktree Manager

A fast, simple Git worktree helper written in Go. 
Inspired by [haacked/dotfiles/tree-me](https://github.com/haacked/dotfiles/blob/main/bin/tree-me).

## Features

- Organized worktree structure: `~/dev/worktrees/<repo>/<branch>`
- Simple commands for common worktree operations
- GitHub PR checkout support (via `gh` CLI)
- Shell integration with auto-cd functionality
- Tab completion for Bash and Zsh

## Installation

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

Add this to your `~/.bashrc` or `~/.zshrc`:

```bash
source <(wt shellenv)
```

This enables:
- Automatic `cd` to worktree after `checkout`/`create`/`pr` commands
- Tab completion for commands and branch names

## Usage

### Commands

```bash
# Checkout existing branch in new worktree
wt checkout feature-branch
wt co feature-branch              # short alias

# Create new branch in worktree (defaults to main/master as base)
wt create my-feature
wt create my-feature develop      # specify base branch

# Checkout GitHub PR in worktree (requires gh CLI)
wt pr 123
wt pr https://github.com/org/repo/pull/123

# List all worktrees
wt list
wt ls                             # short alias

# Remove a worktree
wt remove old-branch
wt rm old-branch                  # short alias

# Clean up stale worktree administrative files
wt prune

# Show shell integration code
wt shellenv

# Show help
wt --help
wt <command> --help
```

### Examples

```bash
# Create a new feature branch from main
wt create add-auth-feature

# Checkout an existing branch
wt checkout bugfix-login

# Work on a PR
wt pr 456

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
- Go 1.21+ (for building from source)
- `gh` CLI (optional, only needed for `wt pr` command)
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

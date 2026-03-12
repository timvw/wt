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

- Configurable worktree strategies: `global`, `sibling-repo`, `parent-branches`, and more
- Simple commands for common worktree operations
- **Interactive selection menus** for checkout, remove, pr, and mr commands
- GitHub PR support via `wt pr` command (uses `gh` CLI) — checks out the PR's actual branch name
- GitLab MR support via `wt mr` command (uses `glab` CLI) — checks out the MR's actual branch name
- **Pre/post command hooks** — run custom scripts (e.g. copy `.env`, install deps) on create/checkout/remove
- Shell integration with auto-cd functionality
- Tab completion for Bash and Zsh

## Installation

### Homebrew (macOS and Linux)

```bash
brew install timvw/tap/wt
wt init  # Configure shell integration
```

### Scoop (Windows)

```powershell
scoop bucket add timvw https://github.com/timvw/scoop-bucket
scoop install wt
wt init  # Configure shell integration
```

### WinGet (Windows)

```powershell
winget install timvw.wt
wt init  # Configure shell integration
```

### Linux Packages

Download `.deb`, `.rpm`, or `.pkg.tar.zst` packages from the [releases page](https://github.com/timvw/wt/releases).

```bash
# Debian/Ubuntu
sudo dpkg -i wt_*.deb

# Fedora/RHEL
sudo rpm -i wt_*.rpm

# Arch Linux (AUR)
yay -S wt-bin
```

Shell integration is automatically configured during package installation.

### From Source

```bash
go install github.com/timvw/wt@latest
wt init  # Configure shell integration
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

# Configure shell integration
wt init
```

### Shell Integration

The `wt init` command automatically configures shell integration for your shell:

```bash
wt init              # Auto-detect shell and configure
wt init bash         # Configure for bash specifically
wt init zsh          # Configure for zsh specifically
wt init --dry-run    # Preview changes without modifying files
wt init --uninstall  # Remove wt configuration from shell
```

After running `wt init`, restart your shell or run:

```bash
source ~/.bashrc   # for bash
source "${ZDOTDIR:-$HOME}/.zshrc"    # for zsh
```

Shell integration enables:

- Automatic `cd` to worktree after `checkout`/`create`/`pr`/`mr` commands
- Tab completion for commands and branch names

**Manual setup** (alternative to `wt init`): Add this to the **END** of your shell config:

```bash
eval "$(wt shellenv)"
```

**Note for zsh users:** Place this after `compinit` in your config file.


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

# Checkout GitHub PR in worktree using the PR's branch name (requires gh CLI)
wt pr 123                                          # looks up branch for PR #123
wt pr https://github.com/org/repo/pull/123         # GitHub PR URL
wt pr                                              # interactive: select from open PRs

# Checkout GitLab MR in worktree using the MR's branch name (requires glab CLI)
wt mr 123                                          # looks up branch for MR !123
wt mr https://gitlab.com/org/repo/-/merge_requests/123  # GitLab MR URL
wt mr                                              # interactive: select from open MRs

# List all worktrees
wt list
wt ls                             # short alias

# Remove a worktree
wt remove old-branch
wt rm old-branch                  # short alias
wt rm                             # interactive: select from existing worktrees

# Migrate existing worktrees to configured paths
wt migrate                        # migrate now
wt migrate --force                # force when target path exists
# If the primary checkout is under WORKTREE_ROOT, migrate moves it to ~/src/<owner>/<repo>
# (or ~/src/<repo> when remote owner is unavailable) and repairs linked worktrees.

# Clean up stale worktree administrative files
wt prune

# Configure shell integration
wt init
wt init --uninstall   # Remove shell integration

# Show shell integration code (for manual setup)
wt shellenv

# Show version
wt version

# Show worktree location configuration
wt info

# Manage configuration file
wt config init          # Create a default config file
wt config show          # Show effective configuration with sources
wt config path          # Print the config file path

# Show help
wt --help
wt <command> --help

# Show practical examples
wt examples

# Use machine-readable output
wt --format json version
wt --format json list
wt --format json
```

### JSON Output (`--format json`)

Most commands support machine-readable JSON output:

```bash
wt --format json version
wt --format json info
wt --format json config show
wt --format json list
wt --format json examples
wt --format json          # root help in JSON envelope
```

Important behavior for shell integration:

- In `text` mode (default), shell integration may auto-navigate to the target worktree.
- In `json` mode, output is kept machine-readable and shell integration does **not** auto-navigate.

For commands that normally prompt interactively (`wt co`, `wt rm`, `wt pr`, `wt mr`), pass explicit arguments when using `--format json`.

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

# Interactive PR checkout — resolves to the PR's branch name (requires gh CLI)
$ wt pr
Use the arrow keys to navigate: ↓ ↑ → ←
? Select Pull Request:
  ▸ #123: Add authentication feature
    #124: Update documentation
    #125: Fix login bug
# e.g. selecting #123 creates worktree at ~/dev/worktrees/<repo>/feat/add-auth

# Interactive MR checkout — resolves to the MR's branch name (requires glab CLI)
$ wt mr
Use the arrow keys to navigate: ↓ ↑ → ←
? Select Merge Request:
  ▸ !456: Add authentication feature
    !457: Update documentation
    !458: Fix login bug
# e.g. selecting !456 creates worktree at ~/dev/worktrees/<repo>/feat/add-auth
```

### Examples

```bash
# Create a new feature branch from main
wt create add-auth-feature

# Checkout an existing branch
wt checkout bugfix-login

# Work on a GitHub PR (checks out the PR's branch, e.g. feat/add-auth)
wt pr 456

# Work on a GitLab MR (checks out the MR's branch, e.g. fix/api-cleanup)
wt mr 789

# List all your worktrees
wt list

# Remove a worktree when done
wt rm add-auth-feature

# Show full examples catalog (filter with rg/grep if needed)
wt examples

# Each example includes outcome + path illustration based on config pattern
# e.g. path example: $WORKTREE_ROOT/<repo>/<branch> -> (removed)
# and concrete text/json output samples where relevant

# JSON mode does not auto-navigate; use returned navigate_to
wt --format json create add-auth-feature
```

## Configuration

### Configuration File

`wt` supports an optional TOML configuration file. Use `wt config` commands to manage it:

```bash
wt config init          # Create a default config file
wt config init --force  # Overwrite existing config file
wt config show          # Show effective configuration with sources
wt config path          # Print the config file path
```

**File location** (in order of priority):

1. `--config` flag: `wt --config /path/to/config.toml <command>`
2. `WT_CONFIG` environment variable
3. Default: `~/.config/wt/config.toml` (respects `$XDG_CONFIG_HOME`; `%AppData%\wt\config.toml` on Windows)

**Example config file** (`~/.config/wt/config.toml`):

```toml
# Root directory for worktrees (default: ~/dev/worktrees)
root = "~/projects/worktrees"

# Worktree placement strategy
strategy = "sibling-repo"

# Separator replaces "/" and "\" in value variables ({.branch}, {.repo.Owner}, {.env.*})
# Default "/" preserves slashes (nested dirs). Set to "-" or "_" for flat paths.
# separator = "/"

# Custom pattern (used when strategy = "custom", or to override any strategy's default)
# pattern = "{.worktreeRoot}/{.repo.Name}/{.branch}"
```

### Precedence

Configuration values are resolved in this order (highest priority first):

1. **CLI flags** (`--config`)
2. **Environment variables** (`WORKTREE_ROOT`, `WORKTREE_STRATEGY`, `WORKTREE_PATTERN`, `WORKTREE_SEPARATOR`)
3. **Config file** (`~/.config/wt/config.toml`)
4. **Built-in defaults**

Run `wt config show` to see the effective value and source of each setting.

### Worktree Location

By default, worktrees are created at `~/dev/worktrees/<repo>/{.branch}` using the `global` strategy.

Configure the location with environment variables or the config file:

- `WORKTREE_ROOT` / `root` (default: `~/dev/worktrees`)
- `WORKTREE_STRATEGY` / `strategy` (`global`, `sibling-repo`, `parent-branches`, `parent-worktrees`, `parent-dotdir`, `inside-dotdir`, `custom`)
- `WORKTREE_PATTERN` / `pattern` (optional; overrides the default structure within the chosen strategy)
- `WORKTREE_SEPARATOR` / `separator` (default: `/`; controls how `/` and `\` in value variables are replaced)

Available pattern variables:

- `{.repo.Name}` repo name
- `{.repo.Main}` main branch worktree path (path variable, not transformed by separator)
- `{.repo.Owner}` repo owner/group (from origin URL)
- `{.repo.Host}` git host (from origin URL)
- `{.branch}` git branch name
- `{.worktreeRoot}` value of `WORKTREE_ROOT` (path variable, not transformed by separator)
- `{.env.VARNAME}` value of environment variable `VARNAME` (e.g. `{.env.USER}`, `{.env.HOME}`)

Default patterns per strategy:

| Strategy | Description | Default pattern |
| --- | --- | --- |
| `global` | worktrees under a global directory | `{.worktreeRoot}/{.repo.Name}/{.branch}` |
| `sibling-repo` | worktrees next to the main repo directory | `{.repo.Main}/../{.repo.Name}-{.branch}` |
| `parent-branches` | branches as siblings of main | `{.repo.Main}/../{.branch}` |
| `parent-worktrees` | branches under `<repo>.worktrees/` | `{.repo.Main}/../{.repo.Name}.worktrees/{.branch}` |
| `parent-dotdir` | branches under `.worktrees/` next to main | `{.repo.Main}/../.worktrees/{.branch}` |
| `inside-dotdir` | branches under `.worktrees/` inside main | `{.repo.Main}/.worktrees/{.branch}` |
| `custom` | user-defined pattern | `WORKTREE_PATTERN` |

### Separator

The `separator` setting controls how `/` and `\` characters in **value variables** (`{.branch}`, `{.repo.Owner}`, `{.env.*}`) are replaced. **Path variables** (`{.repo.Main}`, `{.worktreeRoot}`) are never transformed.

| Separator | Branch `feat/foo` becomes | Use case |
| --- | --- | --- |
| `/` (default) | `feat/foo` (nested dirs) | Standard layout |
| `-` | `feat-foo` (flat) | Sibling-repo, flat directories |
| `_` | `feat_foo` (flat) | Alternative flat layout |
| `""` | `featfoo` | Compact (rarely used) |

```toml
# ~/.config/wt/config.toml
separator = "-"   # feat/foo -> feat-foo
```

```bash
export WORKTREE_SEPARATOR="-"
```

Customize the location via environment variables:

```bash
export WORKTREE_ROOT="$HOME/projects/worktrees"
export WORKTREE_STRATEGY="sibling-repo"
export WORKTREE_SEPARATOR="-"
export WORKTREE_PATTERN="{.repo.Main}/../{.repo.Name}/{.branch}"
```

Or via config file:

```toml
root = "~/projects/worktrees"
strategy = "sibling-repo"
separator = "-"
pattern = "{.repo.Main}/../{.repo.Name}/{.branch}"
```

Run `wt info` to see the active strategy, pattern, and available variables.

### Hooks

Hooks let you run custom commands before or after `wt` operations. Define them in the `[hooks]` section of your config file:

```toml
# ~/.config/wt/config.toml
[hooks]
post_create = ["test -f $WT_MAIN/.env && cp $WT_MAIN/.env $WT_PATH/.env || true"]
post_checkout = ["cd $WT_PATH && npm install"]
pre_remove = ["echo Removing worktree at $WT_PATH"]
```

**Available hooks:**

| Hook | When it runs |
| --- | --- |
| `pre_create` / `post_create` | Before/after `wt create` |
| `pre_checkout` / `post_checkout` | Before/after `wt checkout` (alias `wt co`) |
| `pre_remove` / `post_remove` | Before/after `wt remove` (alias `wt rm`) |
| `pre_pr` / `post_pr` | Before/after `wt pr` |
| `pre_mr` / `post_mr` | Before/after `wt mr` |

Hooks only run when a **new worktree is actually created** (or removed). If a worktree already exists, the early-return path skips hooks entirely.

**Environment variables** available in hook commands:

| Variable | Description |
| --- | --- |
| `$WT_PATH` | Worktree path being created/removed |
| `$WT_BRANCH` | Branch name |
| `$WT_MAIN` | Path to the main worktree |
| `$WT_REPO_NAME` | Repository name |
| `$WT_REPO_HOST` | Git host (e.g. `github.com`) |
| `$WT_REPO_OWNER` | Repository owner/group |

**Behavior:**

- **Pre-hooks** abort the operation if any command exits non-zero
- **Post-hooks** print a warning on failure but do not fail the `wt` command
- Each hook is a list of shell commands executed via `sh -c` (or `cmd /c` on Windows)
- Set `WT_HOOKS_DISABLED=1` to skip all hooks (useful for scripting or CI)

**Common patterns:**

```toml
[hooks]
# Copy .env file to new worktrees (only if it exists in main)
post_create = ["test -f $WT_MAIN/.env && cp $WT_MAIN/.env $WT_PATH/.env || true"]

# Install dependencies after checkout
post_checkout = ["cd $WT_PATH && npm install"]

# Run cleanup before removing a worktree
pre_remove = ["cd $WT_PATH && npm run clean"]
```

### Example: Task spanning multiple repositories

When a task or story requires changes across multiple repositories (e.g. a shared library and a main application), you can organize worktrees by feature instead of by repo using a custom pattern:

```toml
# ~/.config/wt/config.toml
strategy = "custom"
pattern = "{.worktreeRoot}/{.branch}/{.repo.Name}"
```

Use the same branch name in each repository:

```bash
cd ~/src/shared-lib
wt create feat/PROJ-123

cd ~/src/main-app
wt create feat/PROJ-123
```

This groups all repositories for a task together:

```
~/dev/worktrees/
  feat/PROJ-123/
    shared-lib/
    main-app/
```

### Example: Using environment variables in patterns

You can reference any environment variable in your pattern with `{.env.VARNAME}`. This lets you group worktrees by an external value such as a feature name, ticket ID, or sprint.

```toml
# ~/.config/wt/config.toml
strategy = "custom"
pattern = "{.worktreeRoot}/{.env.FEATURE}/{.repo.Name}"
```

Set the `FEATURE` environment variable and create worktrees across repos:

```bash
export FEATURE=PROJ-42-new-checkout

cd ~/src/frontend
wt create main        # any branch name works; layout is driven by FEATURE

cd ~/src/backend
wt create main
```

This groups all repositories for a feature together:

```
~/dev/worktrees/
  PROJ-42-new-checkout/
    frontend/
    backend/
```

Switch to a different feature by changing the variable:

```bash
export FEATURE=PROJ-99-hotfix

cd ~/src/frontend
wt create main

cd ~/src/backend
wt create main
```

```
~/dev/worktrees/
  PROJ-42-new-checkout/
    frontend/
    backend/
  PROJ-99-hotfix/
    frontend/
    backend/
```

If the referenced environment variable is not set, `wt` will return an error.

## Development

The project includes a `justfile` for common build tasks. Install [just](https://github.com/casey/just) to use it.

Available tasks:

```bash
just              # Show available recipes
just build        # Build the binary
just test         # Run unit tests
just e2e          # Run E2E tests
just test-all     # Run unit + E2E tests
just clean        # Clean build artifacts
just build-all    # Cross-compile for multiple platforms
just dev-shellenv # Print shell integration for running from source
```

### Running from source with shell integration

When hacking on `wt`, you can use `dev-shellenv` to get a shell function that
runs directly from source instead of an installed binary. This means every `wt`
command instantly reflects your code changes — no rebuild or reinstall needed.

```bash
eval "$(just dev-shellenv)"
```

This replaces the `wt` shell function so it calls `go run` against your local
checkout. Auto-cd and tab completion work as normal.

Re-run the `eval` line after changing the shell completion code in `shellenv`.

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

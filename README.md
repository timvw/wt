# wt - Git Worktree Manager

[![CI](https://github.com/timvw/wt/actions/workflows/ci.yml/badge.svg)](https://github.com/timvw/wt/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/timvw/wt)](https://goreportcard.com/report/github.com/timvw/wt)
[![codecov](https://codecov.io/gh/timvw/wt/branch/main/graph/badge.svg)](https://codecov.io/gh/timvw/wt)
[![Go Reference](https://pkg.go.dev/badge/github.com/timvw/wt.svg)](https://pkg.go.dev/github.com/timvw/wt)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Release](https://img.shields.io/github/v/release/timvw/wt)](https://github.com/timvw/wt/releases)

A fast, simple Git worktree helper written in Go.
Inspired by [haacked/dotfiles/tree-me](https://github.com/haacked/dotfiles/blob/main/bin/tree-me).

![wt quickstart](docs/wt-quickstart.gif)

## Features

- Configurable worktree strategies: `global`, `sibling-repo`, `parent-branches`, and more
- Simple commands for common worktree operations
- **Interactive selection menus** with fuzzy matching for checkout, remove, pr, and mr commands
- GitHub PR support via `wt pr` command (uses `gh` CLI) — checks out the PR's actual branch name
- GitLab MR support via `wt mr` command (uses `glab` CLI) — checks out the MR's actual branch name
- **Pre/post command hooks** — run custom scripts (e.g. copy `.env`, install deps) on create/checkout/remove
- **Stale worktree detection** — find worktrees with deleted remote branches or inactive commits (`wt cleanup --stale`)
- **Color-coded status output** — green (clean), red (dirty), yellow (ahead/behind), bold cyan (current); respects `NO_COLOR=1` and auto-strips colors when piped
- **Per-repo `.wt.toml` config** — override global settings (strategy, hooks, etc.) on a per-repository basis
- Shell integration with auto-cd functionality
- Tab completion for Bash and Zsh

## Quick Start

```bash
brew install timvw/tap/wt   # or: go install github.com/timvw/wt@latest
wt init                      # configure shell integration
```

See [docs/installation.md](docs/installation.md) for all platforms (Scoop, WinGet, Linux packages, from source).

## Usage

### Checkout & Create

```bash
# Checkout existing branch in new worktree
wt co feature-branch
wt co                             # interactive: fuzzy-search from available branches

# Create new branch in worktree (defaults to main/master as base)
wt create my-feature
wt create my-feature develop      # specify base branch
```

### PRs & MRs

```bash
# Checkout GitHub PR (requires gh CLI)
wt pr 123                                          # looks up branch for PR #123
wt pr https://github.com/org/repo/pull/123         # GitHub PR URL
wt pr                                              # interactive: fuzzy-search from open PRs

# Checkout GitLab MR (requires glab CLI)
wt mr 123                                          # looks up branch for MR !123
wt mr https://gitlab.com/org/repo/-/merge_requests/123  # GitLab MR URL
wt mr                                              # interactive: fuzzy-search from open MRs
```

### List & Remove

![wt create](docs/wt-create.gif)

```bash
wt ls                             # list all worktrees
wt rm old-branch                  # remove a worktree
wt rm                             # interactive: fuzzy-search worktree to remove
```

### Maintenance & Misc

```bash
wt migrate                        # migrate worktrees to configured paths
wt migrate --force                # force when target path exists
wt cleanup --stale                # detect stale worktrees (deleted remotes, inactive commits)
wt cleanup --stale --stale-days 7 # custom inactivity threshold (default: 30 days)
wt prune                          # clean up stale worktree admin files
wt version                        # show version
wt examples                       # show practical examples
wt --help                         # show help
```

### Info & Config

![wt info](docs/wt-info.gif)

```bash
wt info                           # show active strategy, pattern, variables
wt config show                    # show effective config with sources (global + repo .wt.toml)
wt config init                    # create a default config file
wt config path                    # print the config file path
# Place a .wt.toml in a repo root to override global config for that repo
```

### Status Dashboard

![wt status](docs/wt-status.gif)

```bash
wt status                         # color-coded overview of all worktrees
```

Shows dirty/clean state, ahead/behind counts, and highlights the current worktree. Colors are automatically stripped when piping; set `NO_COLOR=1` to disable.

### Interactive Selection

![wt interactive](docs/wt-interactive.gif)

When you run `wt co`, `wt rm`, `wt pr`, or `wt mr` without arguments, you'll get an interactive selection menu. Typing filters the results with fuzzy matching, so you can quickly find the branch or worktree you're looking for.

### JSON Output (`--format json`)

Most commands support machine-readable JSON output:

```bash
wt --format json version
wt --format json info
wt --format json config show
wt --format json list
wt --format json examples
```

In `json` mode, shell integration does **not** auto-navigate. For commands that normally prompt interactively, pass explicit arguments when using `--format json`.

## Documentation

| Topic | Description |
| --- | --- |
| [Configuration](docs/configuration.md) | Config file, strategies, patterns, separator, hooks, per-repo `.wt.toml` |
| [Examples](docs/examples.md) | Claude Code + tmux, multi-repo workflows, environment variables |
| [Installation](docs/installation.md) | All platforms, shell integration, building from source |
| [Development](docs/development.md) | Building, testing, running from source |

## How It Works

The tool wraps Git's native worktree commands with a convenient interface and organized directory structure:

1. **Organized Structure**: All worktrees for a repo are kept together
2. **Smart Defaults**: Automatically detects repo name and default branch
3. **Prevents Duplicates**: Checks if a worktree already exists before creating
4. **Auto-CD**: With shell integration, automatically changes to the worktree directory
5. **Tab Completion**: Makes it easy to work with existing branches

## License

MIT

## Credits

Based on [tree-me](https://github.com/haacked/dotfiles/blob/main/bin/tree-me) by Phil Haack.

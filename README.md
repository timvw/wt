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
- **Clone into organized locations** — `wt clone` acquires a repo under `repo_root` (`<host>/<owner>/<repo>/<branch>`), ready to inspect
- Simple commands for common worktree operations
- **Interactive selection menus** with fuzzy matching for checkout, cd, remove, pr, and mr commands
- GitHub PR support via `wt pr` command (uses `gh` CLI) — checks out the PR's actual branch name
- GitLab MR support via `wt mr` command (uses `glab` CLI) — checks out the MR's actual branch name
- **Pre/post command hooks** — run custom scripts on create/checkout/remove/clone (e.g. [launch AI assistants](docs/examples.md#ai-assistants-and-editors), [share build caches](docs/examples.md#shared-build-cache-across-worktrees), [assign dev server ports](docs/examples.md#deterministic-dev-server-port-per-worktree), [copy `.env`](docs/examples.md#copy-env-to-new-worktrees), [init submodules](docs/examples.md#git-submodules-in-worktrees))
- **Stale worktree detection** — find worktrees with deleted remote branches or inactive commits (`wt cleanup --stale`)
- **Color-coded status output** — green (clean), red (dirty), yellow (ahead/behind), bold cyan (current); respects `NO_COLOR=1` and auto-strips colors when piped
- **CI/CD status integration** — `wt status --ci` shows pipeline status (✓/✗/●) per branch via `gh` or `glab` CLI
- **Per-repo `.wt.toml` config** — override global settings (strategy, hooks, etc.) on a per-repository basis
- Shell integration with auto-cd functionality
- Tab completion for Bash, Zsh, and Fish

## Quick Start

```bash
brew install timvw/tap/wt   # or: go install github.com/timvw/wt@latest
wt init                      # configure shell integration
```

See [docs/installation.md](docs/installation.md) for all platforms (Scoop, WinGet, Linux packages, from source).

## Usage

### Clone

```bash
# Acquire the main repository, ready to inspect. Placed under repo_root as
# <repo_root>/<host>/<owner>/<repo>/<default-branch>, left on its default branch
# (that trailing segment makes the clone a normal worktree slot, so wt create
# later puts siblings next to it).
wt clone timvw/wt                              # owner/repo resolved via gh/glab
wt clone git@github.com:me/dotfiles.git        # full URL used as-is
wt clone acme/api ~/src/api                    # explicit destination
```

Two settings control placement: `repo_root` (default `~/dev/repos`) and
`repo_pattern`. Grouping levels like "work" vs "personal" are yours to define —
put an env var in the pattern:

```toml
repo_pattern = "{.repoRoot}/{.env.WT_CATEGORY}/{.repo.Owner}/{.repo.Name}/{.branch}"
```

```bash
export WT_CATEGORY=personal          # default, e.g. in your shell rc
WT_CATEGORY=work wt clone acme/api   # ~/dev/repos/work/acme/api/main
wt clone timvw/wt                    # ~/dev/repos/personal/timvw/wt/main
```

### Checkout & Create

```bash
# Checkout existing branch in new worktree
wt co feature-branch
wt co                             # interactive: fuzzy-search from available branches

# Create new branch in worktree (defaults to main/master as base)
wt create my-feature
wt create my-feature develop      # specify base branch
```

### Switch Between Worktrees

```bash
# Switch to a worktree that already exists (never creates one)
wt cd feature-branch
wt cd                             # interactive: fuzzy-search from existing worktrees
wt sw                             # alias for wt cd
```

Unlike `wt co`, the `wt cd` list contains only branches that already have a worktree — including the main checkout — so it stays short in repositories with many branches.

### PRs & MRs

![wt pr](docs/wt-pr.gif)

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

On case-insensitive filesystems such as the default macOS APFS setup, mixed-case branch
prefixes can produce confusing worktree paths. For example, `Feature/foo` and
`feature/bar` both need a first-level directory that macOS treats as the same name.
Set `separator = "-"` to flatten branch paths (`Feature/foo` -> `Feature-foo`) and
avoid that class of collision. See [Configuration](docs/configuration.md#case-insensitive-filesystems).

### Status Dashboard

![wt status](docs/wt-status.gif)

```bash
wt status                         # color-coded overview of all worktrees
wt status --ci                    # include CI/CD pipeline status (requires gh or glab)
```

Shows dirty/clean state, ahead/behind counts, and highlights the current worktree. With `--ci`, each branch shows ✓ (pass), ✗ (fail), or ● (pending) for its latest CI pipeline. Colors are automatically stripped when piping; set `NO_COLOR=1` to disable.

### Interactive Selection

![wt interactive](docs/wt-interactive.gif)

When you run `wt co`, `wt cd`, `wt rm`, `wt pr`, or `wt mr` without arguments, you'll get an interactive selection menu. Typing filters the results with fuzzy matching, so you can quickly find the branch or worktree you're looking for.

`wt co` selects from every local and remote branch (creating a worktree when needed), while `wt cd` selects only from worktrees that already exist.

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

### Use with Claude Code

Install the [Claude Code plugin](plugins/wt/) to teach Claude how to work with wt-managed worktrees:

```bash
claude plugin marketplace add timvw/wt
claude plugin install wt@wt --scope local
```

Once installed, Claude understands wt commands, worktree strategies, and hooks — so you can ask it to create worktrees, set up hooks for copying `.env` files or running `npm install` / `uv sync`, and follow worktree-based workflows automatically.

See [docs/examples.md](docs/examples.md#ai-assistants-and-editors) for hooks that launch Claude Code in tmux per worktree.

## Documentation

| Topic | Description |
| --- | --- |
| [Configuration](docs/configuration.md) | Config file, strategies, patterns, separator, hooks, per-repo `.wt.toml` |
| [Examples](docs/examples.md) | Claude Code + tmux, multi-repo workflows, environment variables |
| [Installation](docs/installation.md) | All platforms, shell integration, building from source |
| [Development](docs/development.md) | Building, testing, running from source |
| [Claude Code Plugin](plugins/wt/) | Plugin that teaches Claude Code how to work with wt |

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

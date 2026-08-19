---
name: wt
description: "This skill should be used when the user asks about 'wt', 'worktree', 'worktrees', 'wt create', 'wt checkout', 'wt list', 'wt remove', 'wt pr', 'wt mr', or mentions managing git worktrees with wt. Also use when the user asks how wt works, how to use wt commands, or how to organize branches with worktrees."
user-invocable: true
---

# Working with wt - Git Worktree Manager

wt is a fast Git worktree helper written in Go. It wraps `git worktree` with a convenient interface, organized directory structure, and smart defaults. Each branch gets its own directory — no stashing, no branch switching.

## Core Philosophy

**Never switch branches in the main checkout.** Always create a worktree for each task. This keeps the user's workspace clean and allows parallel work on multiple branches.

## Commands

| Command | Purpose |
|---------|---------|
| `wt clone <owner/repo\|url> [dest]` | Clone a repo under `repo_root` (host/owner/repo/branch), on its default branch |
| `wt create <branch> [base]` | Create a new branch in a worktree (defaults to main/master as base) |
| `wt co <branch>` | Checkout an existing branch in a new worktree |
| `wt co` | Interactive: fuzzy-search from available branches |
| `wt cd <branch>` | Switch to an existing worktree (alias: `wt sw`); never creates one |
| `wt cd` | Interactive: fuzzy-search from existing worktrees |
| `wt ls` | List all worktrees |
| `wt rm <branch>` | Remove a worktree |
| `wt rm` | Interactive: fuzzy-search worktree to remove |
| `wt pr [number\|url]` | Checkout a GitHub PR (requires `gh` CLI) |
| `wt pr` | Interactive: fuzzy-search from open PRs |
| `wt mr [number\|url]` | Checkout a GitLab MR (requires `glab` CLI) |
| `wt mr` | Interactive: fuzzy-search from open MRs |
| `wt status` | Color-coded overview of all worktrees |
| `wt status --ci` | Include CI/CD pipeline status per branch |
| `wt info` | Show active strategy, pattern, variables |
| `wt config show` | Show effective config with sources |
| `wt cleanup --stale` | Detect stale worktrees (deleted remotes, inactive commits) |
| `wt prune` | Clean up stale worktree admin files |
| `wt migrate` | Migrate worktrees to match configured paths |
| `wt init` | Configure shell integration |
| `wt examples` | Show practical examples |

## Worktree Layout Strategies

wt supports multiple strategies for organizing worktrees. Configure via `~/.config/wt/config.toml` or per-repo `.wt.toml`.

| Strategy | Layout |
|----------|--------|
| `global` | `<root>/<repo>/<branch>` — all repos share one root |
| `sibling-repo` | `../<repo>-worktrees/<branch>` — worktrees next to repo |
| `parent-branches` | `../<branch>` — branches as siblings of main checkout |

The `pattern` setting controls the path template. Variables: `{root}`, `{repo}`, `{branch}`, `{host}`, `{owner}`.

## Configuration

- Config file: `~/.config/wt/config.toml` (or `WT_CONFIG` / `--config`)
- Per-repo override: `.wt.toml` in the repo root
- Git config: `git config --local wt.strategy sibling-repo` (also `wt.root`, `wt.pattern`, `wt.separator`; scalars only, no hooks)
- Key settings: `root`, `strategy`, `pattern`, `separator`
- Env overrides: `WORKTREE_ROOT`, `WORKTREE_STRATEGY`, `WORKTREE_PATTERN`, `WORKTREE_SEPARATOR`
- Precedence (highest first): env > local git config > `.wt.toml` > config file > global git config > defaults

## Hooks

wt supports pre/post hooks for `create`, `checkout`, `remove`, `pr`, `mr`, and `clone` commands.

Configure in `config.toml` or `.wt.toml`:

```toml
[hooks]
post_create = ["cp .env $WT_PATH/.env"]
post_checkout = ["echo 'Switched to $WT_BRANCH'"]
```

Hook environment variables: `WT_PATH`, `WT_BRANCH`, `WT_MAIN`, `WT_REPO_NAME`, `WT_REPO_HOST`, `WT_REPO_OWNER`. Disable all hooks: `WT_HOOKS_DISABLED=1`.

### Hook trust

`.wt.toml` is committed, so its hooks come from the repository rather than from the user. wt does not run them until they are approved:

```bash
wt trust          # approve this repository's .wt.toml hooks
wt trust --list   # show every approval on this machine
wt untrust        # revoke this repository's approval
```

The approval is pinned to (repository, contents of `.wt.toml`), so editing the file — or pulling a commit that adds a hook — asks again. Non-interactive runs (scripts, CI, `--format json`) decline and warn on stderr, unless `WT_HOOKS_APPROVE_ALL=1` is set. Hooks from the user's own `config.toml` are not gated.

Set `hooks_policy` in `config.toml` (or `WT_HOOKS_POLICY`) to `prompt-untrusted` (default), `prompt-all` (confirm every hook, including the user's own), `trusted-only` (never prompt, skip anything unapproved), or `off`. It is never read from `.wt.toml`.

### Common Hook Recipes

**Copy `.env` files from main worktree:**

```toml
[hooks]
post_create = [
  "test -f $WT_MAIN/.env && cp $WT_MAIN/.env $WT_PATH/.env || true"
]
post_checkout = [
  "test -f $WT_MAIN/.env && cp $WT_MAIN/.env $WT_PATH/.env || true"
]
```

**Auto-install dependencies (Node.js):**

```toml
[hooks]
post_create = ["cd $WT_PATH && npm install"]
post_checkout = ["cd $WT_PATH && npm install"]
```

**Auto-install dependencies (Python/uv):**

```toml
[hooks]
post_create = ["cd $WT_PATH && uv sync"]
post_checkout = ["cd $WT_PATH && uv sync"]
```

**Launch Claude Code in tmux per worktree:**

```toml
[hooks]
post_create = [
  "tmux new-session -d -s \"$WT_REPO_NAME/$WT_BRANCH\" -c \"$WT_PATH\" \"claude -n '$WT_REPO_NAME/$WT_BRANCH'\" 2>/dev/null; echo \"tmux session: $WT_REPO_NAME/$WT_BRANCH\""
]
pre_remove = [
  "tmux kill-session -t \"$WT_REPO_NAME/$WT_BRANCH\" 2>/dev/null || true"
]
```

**Shared build cache (symlink `node_modules` across worktrees):**

```toml
[hooks]
post_create = [
  "mkdir -p $HOME/.cache/wt/$WT_REPO_NAME/node_modules && ln -sf $HOME/.cache/wt/$WT_REPO_NAME/node_modules $WT_PATH/node_modules && cd $WT_PATH && npm install"
]
```

**Deterministic dev server port per branch:**

```toml
[hooks]
post_create = [
  "printf 'PORT=%d\\n' $(( 3000 + $(printf '%s' \"$WT_BRANCH\" | cksum | cut -d' ' -f1) % 997 )) > $WT_PATH/.env.port"
]
```

## JSON Output

Most commands support `--format json` for machine-readable output:

```bash
wt --format json list
wt --format json info
wt --format json config show
wt --format json version
```

## Shell Integration

After `wt init`, the shell function auto-navigates to worktrees on create/checkout. As an agent, you won't get auto-cd — use the printed worktree path explicitly in subsequent operations.

## Agent Workflow

```bash
# 1. Create worktree for a task
wt create feat/my-feature

# 2. Work in the worktree directory (path printed by wt create)
# All file operations should use the worktree path

# 3. Run tests, commit, push from the worktree
go test ./...
git add . && git commit -m "feat: my feature"
git push -u origin feat/my-feature

# 4. Create PR
gh pr create --title "feat: my feature" --body "Description"

# 5. Clean up after merge
wt rm feat/my-feature
```

## When Helping Users

- If the user wants to work on a branch, suggest `wt create` or `wt co` instead of `git checkout`
- Use `wt ls` to understand the current worktree layout
- For PRs/MRs, use `wt pr` / `wt mr` — they resolve the branch name automatically
- Use `wt status` to get an overview of all worktrees and their state
- For non-interactive/agent contexts, always pass explicit arguments (e.g., `wt co <branch>`, not `wt co`)

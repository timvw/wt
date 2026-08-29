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
| `wt copy [branch]` | Materialise the `[files]` set into a worktree (`--dry-run`, `--force`, `--from`) |
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

The `pattern` setting controls the path template. Variables: `{.worktreeRoot}`, `{.repo.Name}`, `{.repo.Main}`, `{.repo.Owner}`, `{.repo.Host}`, `{.branch}`, `{.env.VARNAME}`. A pattern committed in `.wt.toml` is confined to `root`, including when it renders as an absolute path. Set `wt.pattern` in local git config or `WORKTREE_PATTERN` for an intentional machine-local placement outside that tree.

The dotted form is required: `wt` renders patterns with `missingkey=error`, so a bare `{root}` or `{repo}` is a hard failure rather than an empty segment. Run `wt info` to see the full variable list and the active pattern.

## Configuration

- Config file: `~/.config/wt/config.toml` (or `WT_CONFIG` / `--config`)
- Per-repo override: `.wt.toml` in the repo root
- Git config: `git config --local wt.strategy sibling-repo` (also `wt.root`, `wt.pattern`, `wt.separator`, `wt.copyIgnored` — git config names allow no underscore; scalars only, no hooks, no `[files]` lists)
- Key settings: `root`, `strategy`, `pattern`, `separator`
- Env overrides: `WORKTREE_ROOT`, `WORKTREE_STRATEGY`, `WORKTREE_PATTERN`, `WORKTREE_SEPARATOR`, `WT_COPY_IGNORED`
- Environment variable defaults: `{.env.VARNAME:-fallback}` uses `fallback` when `VARNAME` is unset; `{.env.VARNAME}` without `:-` still errors on missing (catches typos)
- Precedence (highest first): env > local git config > `.wt.toml` > config file > global git config > defaults

## Untracked files (`[files]`)

A new worktree contains everything git tracks and nothing else, so `.env`, `.envrc` and `.claude/settings.local.json` are missing until they are put there. `[files]` declares that once and wt materialises it on `create`, `checkout`, `pr` and `mr` — after `git worktree add`, before the `post_*` hooks.

```toml
[files]
copy = [".env", ".envrc", ".claude/settings.local.json"]  # gitignore syntax
link = ["node_modules", ".venv"]                          # symlink instead of copy
exclude = ["*.pem", "*.key"]                              # applied last, always wins
copy_ignored = false                                      # copy every ignored file
```

A `.worktreeinclude` file at the **main worktree root** holds the same patterns one per line and is unioned into `copy`. It is meant to be committed, so the repo can declare what every contributor's worktrees need.

Key semantics:

- **Nothing is copied by default.** Without configuration, `wt create` behaves as before.
- The three list keys **accumulate** across layers (config file → `.wt.toml` → `.worktreeinclude`) rather than replacing each other. `exclude` is applied last and cannot be overridden, including over `link`. Only `copy_ignored` follows the normal precedence chain.
- A directory pattern covers its whole tree, and a `!` in `copy` wins over a matched parent directory or `copy_ignored` — `copy = ["cache/", "!cache/private.key"]` copies everything under `cache/` except that key. `!` is rejected in `exclude` and `link`, since a committed `.wt.toml` could otherwise undo a global exclude.
- Candidates come from `git ls-files --others --ignored --exclude-standard`, so **tracked files are never copied**. `link` checks the index directly and skips tracked paths too.
- Copies use a reflink on APFS/Btrfs/XFS; symlinks are recreated as symlinks, never dereferenced. A destination whose parent is a symlink is refused, not written through.
- Existing destination files are **skipped, not overwritten**, unless `--force`.
- `[files]` needs no `wt trust` approval: it is declarative data, unlike `[hooks]`.
- Suppress with `--no-copy` on create/checkout/pr/mr, or `WT_FILES_DISABLED=1`.

```bash
wt copy feature-branch --dry-run   # show what would happen, change nothing
wt copy feature-branch --force     # overwrite what is already there
wt copy feature-branch --from other-branch  # seed from a sibling worktree
```

## Hooks

wt supports pre/post hooks for `create`, `checkout`, `remove`, `pr`, `mr`, and `clone` commands.

Prefer `[files]` for copying files — hooks are for running commands.

Configure in `config.toml` or `.wt.toml`:

```toml
[hooks]
post_create = ["cd $WT_PATH && npm install"]
post_checkout = ["echo 'Switched to $WT_BRANCH'"]
```

Hook environment variables: `WT_PATH`, `WT_BRANCH`, `WT_MAIN`, `WT_REPO_NAME`, `WT_REPO_HOST`, `WT_REPO_OWNER`. Disable all hooks: `WT_HOOKS_DISABLED=1`.

### Hook trust

wt runs no hook until it has been approved — a repository's committed `.wt.toml` and the user's own `config.toml` alike (since wt 0.4.0):

```bash
wt trust             # approve every hook source that applies here
wt trust --list      # show every approval on this machine
wt untrust           # revoke this repository's approval
wt untrust --global  # revoke the config file's approval
```

An approval is pinned to the source and the sha256 of that source's hook commands. Repository approvals also carry the common git directory's filesystem identity, so replacing a checkout at the same path asks again; linked worktrees share one identity. Editing a hook command, or pulling a commit that adds one, also asks again; editing anything else in the file does not. Approving `config.toml` once covers every repository; approving a `.wt.toml` covers that repository incarnation only. Non-interactive runs (scripts, CI, `--format json`) decline and warn on stderr, unless `WT_HOOKS_APPROVE_ALL=1` is set.

To skip the prompt for trees the user owns, whitelist paths in `config.toml` — hooks under them run unasked and unrecorded:

```toml
[trust]
prefix = ["~/src/mine"]      # this directory and everything below it
exact  = ["~/src/acme/api"]  # this repository only
```

Set `hooks_policy` in `config.toml` (or `WT_HOOKS_POLICY`) to `prompt-untrusted` (default), `prompt-all` (confirm every hook every time, overriding approvals and `[trust]`), `trusted-only` (never prompt, skip anything unapproved — the CI choice), or `off`. Neither `hooks_policy` nor `[trust]` is ever read from `.wt.toml`.

### Common Hook Recipes

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

**Shared build cache across worktrees** — use `[files] link` rather than a hook:

```toml
[files]
link = ["node_modules"]
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

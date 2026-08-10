# Configuration

## Configuration File

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

## Precedence

Configuration values are resolved in this order (highest priority first):

1. **CLI flags** (`--config`)
2. **Environment variables** (`WORKTREE_ROOT`, `WORKTREE_STRATEGY`, `WORKTREE_PATTERN`, `WORKTREE_SEPARATOR`)
3. **Config file** (`~/.config/wt/config.toml`)
4. **Built-in defaults**

Run `wt config show` to see the effective value and source of each setting.

## Strategies & Patterns

By default, worktrees are created at `~/dev/worktrees/<repo>/{.branch}` using the `global` strategy.

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

## Separator

The `separator` setting controls how `/` and `\` characters in **value variables** (`{.branch}`, `{.repo.Owner}`, `{.env.*}`) are replaced. **Path variables** (`{.repo.Main}`, `{.worktreeRoot}`) are never transformed.

| Separator | Branch `feat/foo` becomes | Use case |
| --- | --- | --- |
| `/` (default) | `feat/foo` (nested dirs) | Standard layout |
| `-` | `feat-foo` (flat) | Sibling-repo, flat directories |
| `_` | `feat_foo` (flat) | Alternative flat layout |
| `""` | `featfoo` | Compact (rarely used) |

### Case-Insensitive Filesystems

The default macOS APFS setup is case-insensitive. That means paths such as
`Feature/foo` and `feature/bar` share the same first path component from the
filesystem's point of view, even though Git branch names are case-sensitive.

With the default separator, branch names with slashes become nested directories:

```text
Feature/make-it-work -> ~/dev/worktrees/repo/Feature/make-it-work
feature/add-logging  -> ~/dev/worktrees/repo/feature/add-logging
```

On a case-insensitive filesystem, `Feature` and `feature` refer to the same
directory. This can make `wt create`, `wt checkout`, `wt remove`, shell
completion, and manual `git checkout` commands appear to disagree about the
current branch or worktree path. When `wt` detects this before creating a
worktree, it prints a warning with the colliding path component.

If your repositories use mixed-case branch prefixes such as `Feature/...`, prefer
a flat path layout:

```toml
separator = "-"
```

That maps branches to paths like:

```text
Feature/make-it-work -> ~/dev/worktrees/repo/Feature-make-it-work
feature/add-logging  -> ~/dev/worktrees/repo/feature-add-logging
```

Changing `separator` affects newly created path calculations. Run `wt migrate`
or recreate existing worktrees if you want current worktrees to use the new
layout.

This avoids collisions between unrelated branch prefixes such as `Feature/...`
and `feature/...`. It does not make case-only branch names safe: `Feature/foo`
and `feature/foo` still map to names that collide on a case-insensitive
filesystem. Avoid case-only branch differences, or place the repository and
worktree root on a case-sensitive filesystem.

## Hooks

Hooks let you run custom commands before or after `wt` operations. Define them in the `[hooks]` section of your config file:

![wt hooks](wt-hooks.gif)

**Available hooks:**

| Hook | When it runs |
| --- | --- |
| `pre_create` / `post_create` | Before/after `wt create` |
| `pre_checkout` / `post_checkout` | Before/after `wt checkout` (alias `wt co`) |
| `pre_remove` / `post_remove` | Before/after `wt remove` (alias `wt rm`) |
| `pre_pr` / `post_pr` | Before/after `wt pr` |
| `pre_mr` / `post_mr` | Before/after `wt mr` |

Checkout hooks (`pre_checkout` / `post_checkout`) run both when a new worktree is created **and** when checking out an existing worktree. Create and remove hooks run only when a worktree is actually created or removed.

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
- Each hook is a list of shell commands. `wt` spawns the shell itself, so the shell you are sitting in does not decide what runs them — see [Which shell runs a hook](#which-shell-runs-a-hook)
- Set `WT_HOOKS_DISABLED=1` to skip all hooks (useful for scripting or CI)

**Which shell runs a hook:**

| Where `wt` runs | Interpreter | Write hooks in |
| --- | --- | --- |
| macOS, Linux | `sh -c` | POSIX shell syntax, `$WT_PATH` |
| Windows, from Git Bash / MSYS2 / Cygwin | `sh -c` | POSIX shell syntax, `$WT_PATH` |
| Windows, from PowerShell or `cmd` | `cmd /c` | cmd syntax, `%WT_PATH%` |

On Windows, `wt` uses `sh` when it can see it is running in a POSIX shell environment (`MSYSTEM` is set, or `$SHELL` names a Unix shell) **and** `sh` is on `PATH`; otherwise it falls back to `cmd /c`. When `sh` is chosen, the path-valued variables `$WT_PATH` and `$WT_MAIN` are handed over with forward slashes (`C:/Users/you/worktrees/repo/branch`) so they survive both shell quoting and any native tool the hook invokes.

The examples below — and everything in [examples.md](examples.md) — are POSIX, so they work as written everywhere except a PowerShell or `cmd` session on Windows. From those, write the cmd equivalent:

```toml
[hooks]
# PowerShell / cmd on Windows: cmd syntax, %VAR% expansion
post_create = ["if exist %WT_MAIN%\\.env copy %WT_MAIN%\\.env %WT_PATH%\\.env"]
```

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

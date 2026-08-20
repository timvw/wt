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

## Per-repo config (`.wt.toml`)

Place a `.wt.toml` in a repository root to override the global config for that
repository. It uses the same format, and is meant for **project policy** —
settings you are happy to commit and share with everyone working on the repo.

```toml
# <repo>/.wt.toml
strategy = "sibling-repo"

[hooks]
post_checkout = ["cd \"$WT_PATH\" && npm install"]
```

Two differences from the global config file:

- `root` is ignored. Worktree roots are a property of your machine, not of the
  project, so a repo cannot dictate where your worktrees land.
- Hooks are merged **per hook**: a hook the repo config does not set keeps the
  value from the global config file.

## Git config

`wt` also reads its settings from `git config`, which is useful for **personal,
machine-local** settings you do not want to commit — the reason it outranks
`.wt.toml`:

```bash
# Just this repo (.git/config — never committed)
git config --local wt.strategy sibling-repo

# All repos (~/.gitconfig)
git config --global wt.root ~/projects/worktrees
```

| Key | Equivalent |
| --- | --- |
| `wt.root` | `root` |
| `wt.repo_root` | `repo_root` |
| `wt.strategy` | `strategy` |
| `wt.pattern` | `pattern` |
| `wt.separator` | `separator` |
| `wt.repo_pattern` | `repo_pattern` |

Notes:

- **Hooks are not read from git config.** Use a config file for those.
- Unlike `.wt.toml`, local git config **may** set `wt.root`: it is your own
  local state rather than project policy arriving through a pull request.
- Linked worktrees share the main repository's `.git/config`, so `--local`
  settings apply from every worktree of that repo.

## Precedence

Configuration values are resolved in this order (highest priority first):

1. **Environment variables** (`WORKTREE_ROOT`, `WORKTREE_STRATEGY`, `WORKTREE_PATTERN`, `WORKTREE_SEPARATOR`, `WT_REPO_ROOT`, `WT_REPO_PATTERN`)
2. **Local git config** (`.git/config`, via `git config --local wt.*`)
3. **Repo config file** (`<repo>/.wt.toml`)
4. **Config file** (`~/.config/wt/config.toml`)
5. **Global git config** (`~/.gitconfig`, via `git config --global wt.*`)
6. **Built-in defaults**

`--config` and `WT_CONFIG` are not a precedence level of their own: they select
*which* TOML file is loaded at level 4. Values in that file are still overridden
by `.wt.toml`, local git config, and environment variables.

Run `wt config show` to see the effective value and source of each setting:

```console
$ wt config show
Config file: ~/.config/wt/config.toml (found)

Effective configuration:
  root          = /Users/you/dev/worktrees     (default)
  strategy      = sibling-repo                 (git config (local))
  pattern       = {.repo.Main}/../{.branch}    (default)
  separator     = "/"                          (config file)
  hooks_policy  = prompt-untrusted             (default)
```

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
- `{.env.VARNAME:-default}` value of `VARNAME`, falling back to `default` when unset (e.g. `{.env.WT_CATEGORY:-personal}`)

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

## Clone placement (for `wt clone`)

`wt clone` acquires a repository you don't have yet and puts it in a
predictable spot. Two settings decide where:

```toml
# Base for all clones (default: ~/dev/repos).
repo_root = "~/dev/repos"

# Placement layout. Variables: {.repoRoot}, {.repo.Host}, {.repo.Owner},
# {.repo.Name}, {.branch}, {.env.VARNAME}, {.env.VARNAME:-default}.
# {.branch} is the remote's default branch (via git ls-remote, fallback "main").
repo_pattern = "{.repoRoot}/{.repo.Host}/{.repo.Owner}/{.repo.Name}/{.branch}"
```

`wt clone timvw/wt` then clones into `~/dev/repos/github.com/timvw/wt/main`.
A full git URL is used as-is; an explicit destination argument overrides the
layout entirely. The repository is left on its default branch, ready to
inspect.

The trailing `{.branch}` is deliberate: it makes the clone look like any other
worktree of that repo, so a later `wt create feat/x` can drop a sibling next to
it instead of nesting inside it. Drop it if you prefer a bare
`<owner>/<repo>` checkout.

Both settings are also settable via environment variables — `WT_REPO_ROOT` and
`WT_REPO_PATTERN` — which override the config file. Neither is read from a
repo-level `.wt.toml`: `wt clone` targets a repository other than the one you
are standing in, so letting that repo's config redirect the destination (or run
clone hooks) would be wrong.

### Grouping clones ("categories")

There is no built-in notion of work/personal/oss. If you want that grouping,
express it yourself with an environment variable in the pattern. Use the
`:-` syntax to supply a default so the pattern works even when the variable
is unset:

```toml
repo_pattern = "{.repoRoot}/{.env.WT_CATEGORY:-personal}/{.repo.Owner}/{.repo.Name}/{.branch}"
```

```bash
WT_CATEGORY=work wt clone acme/api   # ~/dev/repos/work/acme/api/main
wt clone timvw/wt                    # ~/dev/repos/personal/timvw/wt/main (default)
```

Without a `:-` default, referencing an unset variable is an error — that is
what catches `{.env.HOEM}` typos. With `:-`, the fallback is used silently.
Setting the variable to the empty string is fine — the segment collapses away
rather than leaving an empty directory level, so `WT_CATEGORY= wt clone timvw/wt`
lands in `~/dev/repos/timvw/wt/main`.

Auth works the same way. `wt clone` resolves `owner/repo` through whichever
account `gh`/`glab` is already using, so select the account the way those tools
intend — `GH_CONFIG_DIR`, `GH_TOKEN`, `GLAB_HOST` — rather than having `wt`
mutate their global state:

```bash
GH_CONFIG_DIR=~/.config/gh-work wt clone acme/api
```

### Setting the category per directory

Passing `WT_CATEGORY=work` on every command gets old, and exporting it in your
shell profile only gives you one value for the whole machine. To say
"everything under this directory is work", use a `[[context]]` rule:

```toml
[[context]]
when_path = "~/dev/repos/work"
env = { WT_CATEGORY = "work" }

[[context]]
when_path = "~/dev/repos/personal"
env = { WT_CATEGORY = "personal" }
```

`when_path` is a **path prefix**, not a glob: a rule matches that directory and
everything beneath it. Matching is on segment boundaries, so `~/dev/repos/work`
does not match `~/dev/repos/workshop`, and symlinks are resolved on both sides.

**Which path is matched** depends on the command:

| Command | Matched against |
| --- | --- |
| `create`, `co`, `pr`, `mr`, `rm`, … | the repository's **main checkout** |
| `clone` | the **current directory** (no repository exists yet) |

Matching the main checkout rather than the current directory is what makes a
category survive the hop between trees. `wt clone acme/api` from
`~/dev/repos/work` lands the checkout in `~/dev/repos/work/acme/api/main`, and a
later `wt create feat/x` resolves `work` from that checkout's path — so the
worktree lands under `~/dev/worktrees/work`, no matter which directory you
happened to run the command from. One rule per category covers both trees.

**Composition.** Every matching rule applies, and later rules override earlier
ones per variable — so a broad rule can set a common value and a narrower one
override a single key:

```toml
[[context]]
when_path = "~/dev/repos"
env = { WT_CATEGORY = "personal", WT_ORG = "timvw" }

[[context]]
when_path = "~/dev/repos/work"
env = { WT_CATEGORY = "work" }      # WT_ORG stays "timvw"
```

**An exported variable always wins**, so the one-off override still works:

```bash
WT_CATEGORY=oss wt create feat/x
```

That includes a variable exported as empty — `WT_CATEGORY= wt clone timvw/wt`
collapses the segment away rather than picking up a rule's value.

Rules are read from **your config file only** — never from a repository's
committed `.wt.toml`, and not from `git config`. A repository you clone must not
be able to redirect where your worktrees land, the same reason `root`,
`repo_root` and `repo_pattern` are excluded from `.wt.toml`.

#### Without `wt` configuration: direnv

If you already use [direnv](https://direnv.net), it does the same job from the
environment side, and `wt` needs no configuration for it at all.

With the category in both patterns:

```toml
pattern = "{.worktreeRoot}/{.env.WT_CATEGORY:-personal}/{.repo.Name}/{.branch}"
repo_pattern = "{.repoRoot}/{.env.WT_CATEGORY:-personal}/{.repo.Owner}/{.repo.Name}/{.branch}"
```

drop an `.envrc` at the head of each category tree — under `repo_root` *and*
under `worktree_root`, since a clone lands in the first and its worktrees in
the second:

```bash
echo 'export WT_CATEGORY=work' > ~/dev/repos/work/.envrc
echo 'export WT_CATEGORY=work' > ~/dev/worktrees/work/.envrc
direnv allow ~/dev/repos/work
direnv allow ~/dev/worktrees/work
```

Every `wt` command run beneath one of those then picks up the category
automatically, and the `:-` default keeps directories without an `.envrc`
working.

Unlike a `[[context]]` rule, direnv keys off the directory you run the command
*from*, which is why both roots need an `.envrc`: a clone lands under
`repo_root` and its worktrees under `worktree_root`, so covering only one loses
the category on the hop between them.

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
| `pre_clone` / `post_clone` | Before/after `wt clone` |

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
- Hooks that came from a repository's committed `.wt.toml` need your approval before they run — see [Hook trust](#hook-trust)
- Set `WT_HOOKS_DISABLED=1` to skip all hooks (useful for scripting or CI)

## Hook trust

`.wt.toml` lives in the working tree, so it is normally committed and travels
with the repository. That makes its `[hooks]` table something the *repository*
supplies rather than something you wrote: without a gate, cloning an untrusted
repo and running `wt create` in it would execute whatever commands that repo
asked for.

So `wt` does not run them until you approve them:

```console
$ wt create feat/x

⚠ These commands come from /home/you/src/acme/.wt.toml (not trusted):

    [post_create] cd "$WT_PATH" && npm install

? Run these hooks?
  ▸ Skip these commands
    Run once
    Run, and trust this .wt.toml until it changes
```

With no terminal to ask on — scripts, CI, `--format json` — the answer is
"skip" unless `WT_HOOKS_APPROVE_ALL=1` is set, and `wt` says so on stderr rather
than failing the command.

Approve ahead of time, or review what is approved:

```bash
wt trust          # approve this repository's .wt.toml
wt trust --list   # every approval on this machine
wt untrust        # revoke this repository's approval
```

An approval is pinned to the file's contents and to the repository. Editing
`.wt.toml` — including a `git pull` that adds a hook, or checking out a branch
whose `.wt.toml` differs — invalidates it and `wt` asks again. An identical
`.wt.toml` in a *different* repository is not covered either: `make setup` is
only as safe as the Makefile next to it.

Approvals are stored in `~/.config/wt/trust.toml` (`$XDG_CONFIG_HOME/wt/` or
`%APPDATA%\wt\` if set). Deleting that file revokes everything.

Hooks from your **own** config file are not gated — you wrote them.

**Requiring approval for every hook:**

```toml
hooks_policy = "prompt-untrusted"   # default
```

| Value | Behaviour |
| --- | --- |
| `prompt-untrusted` | Your own hooks run; hooks from a repo's `.wt.toml` need approval |
| `prompt-all` | Every hook batch is shown and confirmed, whatever supplied it |
| `trusted-only` | Never prompts: already-trusted and own hooks run, anything else is skipped |
| `off` | No hooks run at all (same as `WT_HOOKS_DISABLED=1`) |

`prompt-all` covers what trust alone does not: your own
`post_checkout = ["cd $WT_PATH && npm install"]` runs whatever lifecycle
scripts are in the `package.json` of whichever repository you happen to be
standing in.

Override per invocation with `WT_HOOKS_POLICY`. `hooks_policy` is read from
your config file only — never from a repo-level `.wt.toml`, since a repository
choosing how closely `wt` scrutinises that same repository's hooks would defeat
the point.

For automation you control, `WT_HOOKS_APPROVE_ALL=1` approves every batch
without asking. It bypasses the untrusted-repo check too, so do not export it
in your shell rc.

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

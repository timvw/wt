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
| `wt.copyIgnored` | `[files] copy_ignored` (see [Files](#files)) |
| `wt.context.<name>.whenpath` / `.env` | `[[context]]` (**`--global` only**) |

Notes:

- **Hooks are not read from git config.** Use a config file for those.
- Unlike `.wt.toml`, local git config **may** set `wt.root`: it is your own
  local state rather than project policy arriving through a pull request.
- Linked worktrees share the main repository's `.git/config`, so `--local`
  settings apply from every worktree of that repo.
- `wt.context.*` is the exception to that: it is read from `--global` only.
  See [Setting the category per directory](#setting-the-category-per-directory).

## Precedence

Configuration values are resolved in this order (highest priority first):

1. **Environment variables** (`WORKTREE_ROOT`, `WORKTREE_STRATEGY`, `WORKTREE_PATTERN`, `WORKTREE_SEPARATOR`, `WT_REPO_ROOT`, `WT_REPO_PATTERN`, `WT_COPY_IGNORED`)
2. **Local git config** (`.git/config`, via `git config --local wt.*`)
3. **Repo config file** (`<repo>/.wt.toml`)
4. **Config file** (`~/.config/wt/config.toml`)
5. **Global git config** (`~/.gitconfig`, via `git config --global wt.*`)
6. **Built-in defaults**

`--config` and `WT_CONFIG` are not a precedence level of their own: they select
*which* TOML file is loaded at level 4. Values in that file are still overridden
by `.wt.toml`, local git config, and environment variables.

Two settings are lists rather than scalars and so do not "win" — `[hooks]` and
the `[files]` list keys accumulate across layers instead. See
[Files › Accumulation](#accumulation).

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

#### In global git config

Rules can live in `~/.gitconfig` instead, if you would rather not keep a `wt`
config file at all:

```bash
git config --global wt.context.work.whenpath "~/dev/repos/work"
git config --global --add wt.context.work.env "WT_CATEGORY=work"
git config --global --add wt.context.work.env "WT_ORG=acme"
```

`wt.context.<name>.<key>` is a git subsection, so `<name>` is just a handle for
the rule — it lets you come back and remove one:

```bash
git config --global --remove-section 'wt.context.work'
```

Three things to know:

- **`env` is multi-valued.** Use `--add` once per variable; plain `git config`
  replaces the whole key. Each value is a single `NAME=VALUE` pair, and one
  without an `=` is ignored.
- **git lowercases key names**, so it is `whenpath`, not `whenPath`. A
  camel-cased spelling written by hand into `~/.gitconfig` is silently ignored.
  (Subsection names keep their case, so `<name>` can be spelled however you
  like.)
- **The config file wins.** Global git config sits *below* the config file in
  the [precedence](#precedence) order — level 5 against level 4. This is the
  reverse of `--local` git config at level 2, so "git config beats the config
  file" only holds for `--local`.

The two sources compose rather than replace: rules from `~/.gitconfig` are
evaluated first, then those from the config file, under the same "later
definitions win per variable" rule as above. So a config-file rule overrides a
git config rule wherever both cover the same path, while a git config rule for
an unrelated tree keeps working.

#### Where rules may not come from

Not from a repository's committed `.wt.toml`, and not from `--local` git config.
A repository you clone must not be able to redirect where your worktrees land —
the same reason `root`, `repo_root` and `repo_pattern` are excluded from
`.wt.toml`. `--local` is also the wrong shape for the job: a rule scoped to one
repository is redundant, since that repository could set `wt.pattern` directly.

The system scope (`git config --system`) is not read either — `wt` reads no
system git config at all, for any setting.

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

## Files

A fresh worktree contains everything git tracks and nothing else. The files that
make a checkout *usable* — `.env`, `.envrc`, `.claude/settings.local.json`,
editor state — are exactly the ones you keep out of git, so every new worktree
starts subtly broken until you copy them across by hand.

The `[files]` section declares what should be materialised into every new
worktree. It runs automatically on `wt create`, `wt checkout`, `wt pr` and
`wt mr`, after `git worktree add` and before the `post_*` hooks — and on any of
those four when the worktree already exists, which is what lets one made before
`[files]` was configured catch up. Re-running it costs nothing: files already
there are skipped, never overwritten.

```toml
[files]
# Untracked/ignored paths to copy from the main worktree. gitignore syntax.
copy = [".env", ".envrc", ".claude/settings.local.json"]

# Paths to symlink instead of copy — for large caches you want shared.
link = ["node_modules", ".venv"]

# Never materialised, whatever anything else says. Applied last.
exclude = ["*.pem", "*.key"]

# Copy every ignored file. Off by default; think before turning it on.
copy_ignored = false
```

**Nothing is copied by default.** Without a `[files]` section, a
`.worktreeinclude` file, or `copy_ignored = true`, `wt create` behaves exactly as
it did before.

### `.worktreeinclude`

A `.worktreeinclude` file at the **main worktree root** lists the same kind of
patterns, one per line, in gitignore syntax:

```gitignore
# Untracked files that every worktree needs
.env
.envrc
.claude/settings.local.json
!.claude/**/*.log
```

Its patterns are unioned into `copy`. The name is shared with worktrunk, gtr and
Claude Code's worktree support, so a repo that adds one gets working behaviour
from all of them at once.

Unlike `.wt.toml`, this file is meant to be committed: it describes what the
*project* needs, so a new contributor gets working worktrees without configuring
anything. Because it is committed, it has to be a regular file — a
`.worktreeinclude` that is a symlink is refused rather than followed, so a repo
cannot point it at something outside the worktree.

### Accumulation

The three list keys **accumulate** across layers rather than replacing each
other:

```
config file  →  repo .wt.toml  →  .worktreeinclude
```

A user whose own config says "always copy `.env`" and who then works in a repo
whose `.wt.toml` adds `config/local.yml` gets both. With replace semantics the
repo would silently drop the `.env`. Excludes accumulate for the mirror-image
reason: a global "never copy `*.pem`" has to hold against any repository's
config.

Duplicates are dropped, keeping first-seen order, so `wt info` lists the
effective set in layer order with the source of each pattern.

`exclude` is always applied **last** and cannot be overridden — not by a later
layer's `copy`, not by `copy_ignored`, and not by `link`. An excluded path is
reported as skipped rather than materialised.

Because the layers accumulate with the repo's `.wt.toml` applied *after* your own
config, a `!` in `exclude` would let a committed file undo the protection you set
globally. So `exclude` and `link` reject negated patterns outright, naming the
pattern and the layer it came from. `!` remains available in `copy`, where it
only ever removes.

A directory pattern covers everything below it, in both lists: `exclude =
["secrets/"]` keeps `secrets/key.pem` out, and `copy = ["cache/"]` brings the
whole tree in.

A `!` in `copy` names a path you do *not* want, and it wins over any blanket
yes — a matched parent directory or `copy_ignored`:

```toml
[files]
copy = ["cache/", "!cache/private.key"]   # everything under cache/ except that one
```

This is a deliberate divergence from gitignore, where a file cannot be
re-included once a parent directory is excluded. It only ever selects *fewer*
files, which is the safe direction when the thing being materialised is
somebody's `.env`.

`copy_ignored` is a scalar, so it follows the normal
[precedence chain](#precedence) instead: env var `WT_COPY_IGNORED`, local git
config `wt.copyIgnored`, `.wt.toml`, config file, global git config, then the
default `false`. It is the only `[files]` key readable from git config — the list
keys would need `--get-all` handling for multi-valued keys and have no
accumulation story across git scopes, so they stay TOML-only.

Note the spelling: in git config it is `wt.copyIgnored`, not `wt.copy_ignored`.
git config variable names allow only alphanumerics and `-`, and git rejects an
underscore outright — a config *file* containing one fails to parse entirely.
The name is case-insensitive, so `wt.copyignored` works just as well, and as
with any git boolean the value may be omitted to mean true:

```bash
git config --local wt.copyIgnored true
```

### Which files are candidates

Candidates come from `git ls-files --others --ignored --exclude-standard`, so:

- **Tracked files are never copied.** They are already in the new worktree via
  the checkout, and copying them would overwrite it with the main worktree's
  uncommitted working state. `link` names its paths literally rather than
  drawing them from this list, so it checks the index directly and skips
  anything tracked.
- Nested `.gitignore` files, `core.excludesFile` and `.git/info/exclude` all
  work, because git resolves them rather than `wt`.
- `.git/`, `.bzr/`, `.hg/`, `.jj/`, `.pijul/`, `.sl/` and `.svn/` are never
  copied whatever the patterns say, and neither is any path that is itself a
  registered worktree — otherwise the `inside-dotdir` strategy would copy every
  worktree into every new worktree.

### Copy method

Files are cloned with a **reflink** where the filesystem supports it — APFS on
macOS, Btrfs and XFS on Linux — so a multi-gigabyte `node_modules` costs
metadata rather than disk, and the two copies diverge only as they are written
to. Everywhere else, including Windows, a buffered copy is used. `--format json`
reports the `method` per file (`reflink`, `copy` or `symlink`), so you can check
which you got. `--dry-run` predicts it without writing to the source worktree:
it compares filesystems first and probes inside the destination only.

An existing destination file is **skipped, never overwritten**, unless you pass
`--force`. Symlinks are recreated as symlinks and never dereferenced, and a
destination whose parent directory is a symlink is refused outright rather than
written through — a worktree can legitimately contain a tracked symlink.

A path the **destination branch tracks** is never written, `--force` included:
candidates are the source worktree's ignored files, and one branch's untracked
`.env` is no reason to put an uncommitted file where another branch keeps a
committed one. That holds whether or not the file is currently present there —
deleted, or left out by a sparse checkout, it is still tracked. The same applies
below a tracked path: if the destination branch keeps `cache` as a file, no
`cache/…` is written over it. Those paths are reported as skipped with the
reason `tracked by git in the destination`.

On a **case-insensitive destination** — the macOS and Windows default, and also
an ext4 casefold directory or a CIFS mount, which wt probes for rather than
assumes — two source paths that differ only in case are one path there. A source checked
out on a case-sensitive filesystem can hold both `Cache/` and `cache/`; only the
first is materialised, and the second, along with anything under it, is reported
as skipped with the reason `case-insensitive collision with <path>` rather than
merged into the tree that got there first.

### `wt copy`

The same materialisation on demand — after adding a pattern, or after the source
file changed:

```console
$ wt copy --dry-run          # show what would happen, change nothing
would copy   .env   (reflink)
would skip   .envrc (exists)
would mkdir  cache  (directory)

$ wt copy                    # into the current worktree, from the main one
Copied 1 file (1 reflinked), created 1 directory

$ wt copy feature-branch --force        # overwrite what is already there
$ wt copy feature-branch --from other   # seed from a sibling worktree
```

`--dry-run` leaves both worktrees exactly as it found them, with one deliberate
exception: to report `(reflink)` rather than guess, it clones a probe file
inside the *destination* worktree and removes it again. Whether a filesystem
supports reflink cannot be established any other way — the type is not enough,
since XFS only supports it when the filesystem was created with `reflink=1`. The
probe never touches the source worktree, which may well be read-only, and its
result is cached for the run.

### Turning it off

- `--no-copy` on `wt create`, `wt checkout`, `wt pr` and `wt mr` skips it once.
- `WT_FILES_DISABLED=1` switches the feature off entirely, mirroring
  `WT_HOOKS_DISABLED`.

Failure is never fatal: an unreadable file is reported as `failed` and the
worktree survives. You do not lose a worktree over a missing `.env`.

### Why `[files]` needs no trust prompt

`[hooks]` from a committed `.wt.toml` needs [approval](#hook-trust) because it is
arbitrary command execution. `[files]` is declarative data with a much smaller
blast radius, and it is held there by invariants that are individually tested:

| | Invariant |
| --- | --- |
| F1 | Source patterns resolve strictly inside the main worktree |
| F2 | Destination paths resolve strictly inside the new worktree |
| F3 | `..` path segments are rejected at config-resolution time |
| F4 | Symlinks are copied as symlinks, never dereferenced |
| F5 | Files tracked by git are never copied |
| F6 | An existing destination is never overwritten without `--force` |
| F7 | Nothing outside the two worktrees and `TMPDIR` is read or written |

A hostile `.wt.toml` can therefore ask for your `.env` to be copied from your own
main worktree into your own new worktree — which is where it already was — and
nothing else. Gating it behind a prompt would also defeat the point: the whole
value is that a new worktree just works.

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
post_create = ["cd /d %WT_PATH% && npm install"]
```

Copying files is the one case you no longer need a hook for: [`[files]`](#files)
does it natively, in one shell-independent form that works on Windows too.

**Common patterns:**

```toml
[hooks]
# Install dependencies after checkout
post_checkout = ["cd $WT_PATH && npm install"]

# Run cleanup before removing a worktree
pre_remove = ["cd $WT_PATH && npm run clean"]
```

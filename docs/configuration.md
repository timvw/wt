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

Give an absolute path. A relative `--config` or `WT_CONFIG` names a different
file in every directory `wt` runs in, so whatever is checked out there supplies
one; `wt` says so and reads none. Nor does it help for the path to point out of
the repository — `../wt-user.toml` is outside the submodule you are standing in
and inside the superproject that vendored it.

An absolute path inside the repository you are standing in is refused for the
same reason, usually as a mistake rather than a trick. The name is not the point:
`WT_CONFIG=wt-user.toml` is as easy for a repository to commit as `.wt.toml`.
[`hooks_policy`](#requiring-approval-for-every-hook) and
[`[trust]`](#trusting-paths-ahead-of-time) are honoured from your config file
because it is yours, so a repository read as your config file could exempt itself
from hook approval. Its settings still apply as a repository's, and its hooks
still need `wt trust`.

The path is judged as written, not as it resolves. A config file kept in a
dotfiles repository and symlinked into `~/.config/wt` keeps working, including
while you are standing in that repository — refusing it there would take your
`root` and `pattern` with it and put the worktree somewhere else, and that file
is already your config in every other repository anyway. The one symlink `wt`
does refuse is one whose target is the current repository's own `.wt.toml`: that
file has a repository-side job, and reading it as your config as well would give
it the wider scope, where one approval covers every repository at once.

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

`pattern` **is** read from `.wt.toml`, and one that renders to an absolute path
is not anchored in your `root` at all. That is deliberate — a project may have a
layout in mind — but it means the repository chooses the directory
`git worktree add` writes into. There is one directory it may not choose: `wt`
refuses a pattern landing on its own config directory, on a directory that
contains it, or on your config file — compared with symlinks resolved as far as
each path exists, without regard to case, since `~/.config/WT` is that same
directory on macOS and Windows, and by asking the filesystem which directory
each path really is, since `/System/Volumes/Data/Users/you` is your home
directory under a name that resolves to nothing. The files checked out there
would *become*
`config.toml` and `trust.toml`, and those are the gate itself — the config file
is the one layer hooks are not vetted from, and `trust.toml` is the record of
what you approved. A branch carrying both would not slip a hook past the gate,
it would supply the gate.

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
| `wt.repoRoot` | `repo_root` |
| `wt.strategy` | `strategy` |
| `wt.pattern` | `pattern` |
| `wt.separator` | `separator` |
| `wt.repoPattern` | `repo_pattern` |
| `wt.copyIgnored` | `[files] copy_ignored` (see [Files](#files)) |
| `wt.copy` / `wt.link` / `wt.exclude` | `[files] copy` / `link` / `exclude` (**multi-valued**) |
| `wt.context.<name>.whenpath` / `.env` | `[[context]]` (**`--global` only**) |

The spelling rule: **a git config key is the TOML key with any section dropped
and multi-word names camelCased.** The `[files]` section is not part of the key
— `copy_ignored` is `wt.copyIgnored`, not `wt.files.copyIgnored`. git config
variable names allow only alphanumerics and `-`, so `wt.repo_root` is not a
legal key; see [Key spelling](#key-spelling) for what happens if you write one
anyway. `wt.context.*` is the one key that keeps a section: `<name>` is a git
*subsection*, which is how the key can be repeated once per context.

Notes:

- **Hooks are not read from git config**, at any scope, and neither are
  `hooks_policy` and `[trust]`. That is a security boundary rather than an
  omission — see
  [Why hooks are not readable from git config](#why-hooks-are-not-readable-from-git-config).
- **`wt.copy`, `wt.link` and `wt.exclude` are multi-valued.** Use `--add` once
  per pattern:

  ```bash
  git config --local --add wt.copy .env
  git config --local --add wt.copy .envrc
  ```

  Without `--add`, `git config` refuses to touch a key that already has several
  values: use `--replace-all wt.copy X` to collapse the list to one entry,
  `--unset-all wt.copy` to clear it, or `--unset wt.copy <regex>` to drop one.
  Quote any pattern starting with `!`, which interactive `bash` and `zsh` expand
  as history.
- The three list keys are read from **both** scopes, unlike `wt.context.*`.
  `--global` is for "always bring my `.env`"; `--local` is the per-repo case
  with nothing committed. They carry no power to redirect where a worktree
  lands, so the boundary that keeps `wt.context.*` global-only does not apply.
- An invalid pattern is a hard error for the whole materialisation step, so a
  bad `wt.copy` in `~/.gitconfig` disables file copying in **every** repo on the
  machine. `wt info` names the offending pattern and its layer.
- Unlike `.wt.toml`, local git config **may** set `wt.root`: it is your own
  local state rather than project policy arriving through a pull request.
- Linked worktrees share the main repository's `.git/config`, so `--local`
  settings apply from every worktree of that repo.
- `wt.context.*` is the exception to that: it is read from `--global` only.
  See [Setting the category per directory](#setting-the-category-per-directory).

### Key spelling

git config variable names allow only alphanumerics and `-`. An underscore is not
a spelling variant, it is a parse error — which is why the multi-word keys are
camelCased rather than carried over from TOML verbatim:

```console
$ git config --local wt.repo_root ~/dev/repos
error: invalid key: wt.repo_root
$ git config --local wt.repoRoot ~/dev/repos     # this is the key
```

Names are case-insensitive, so `wt.reporoot` works just as well.

`git config` refuses the bad key, but nothing stops you writing it into
`.git/config` by hand — or generating it, e.g. via home-manager's
`programs.git.extraConfig`, which does not validate key names. One such line
makes git refuse to parse the **whole file**:

```console
$ printf '[wt]\n\trepo_root = /tmp/x\n' >> .git/config
$ git config --local --get wt.strategy
fatal: bad config line 11 in file .git/config
```

`wt` treats an unreadable scope as "nothing configured here", so the symptom is
that every `wt.*` setting in that file quietly stops applying — not an error.
If git config settings seem to be ignored, run `git config --list --local` and
`wt config show` to see which layer is actually supplying each value.

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

The `[files]` list keys do not "win": they accumulate across layers instead, so
a later layer adds to the earlier ones rather than replacing them. See
[Files › Accumulation](#accumulation).

`[hooks]` is neither. Each hook event is resolved on its own, and the highest
layer giving that event a **non-empty** command list supplies the whole list —
a repo `.wt.toml` defining `post_checkout` replaces the config file's
`post_checkout` outright, while leaving every event it does not mention alone.
An empty list reads as "not set" rather than "cleared", so `post_checkout = []`
in `.wt.toml` does not suppress an inherited `post_checkout`; use
`WT_HOOKS_DISABLED=1` or `hooks_policy = "off"` to stop hooks running. See
[Hooks](#hooks).

`hooks_policy` and `[trust]` have no layers at all: both are read from your own
config file and nowhere else, since a repository deciding how closely `wt`
scrutinises that repository's hooks would defeat the point. See
[Hook trust](#hook-trust).

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

The pattern is yours, but the values substituted into it come from the URL you
pasted, so `wt clone` refuses one that tries to choose the destination itself.
A `..` segment in the host, owner, repository name or default branch is
rejected — `https://host/../../tmp/pwn.git` parses to owner `../../tmp`, and an
scp-like `../escape:owner/repo.git` puts it in the host. So is a `$` or `%`
anywhere in those four, or a leading `~`: the rendered path is expanded, so
`https://evil.example/$HOME/.config/wt.git` is not a directory called `$HOME`
but wt's own config directory, and a repository cloned there would supply the
`config.toml` and `trust.toml` that decide whether hooks run. As a backstop the
final destination is refused if it lands on that directory or your config file
however it got there. All of these say to pass an explicit destination, which
is you choosing the path rather than the URL.

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

The three list keys are a **union, not a contest**. Every layer's patterns are
in effect at once, and no layer overrides another:

```
git config (global) → config file → repo .wt.toml → git config (local) → .worktreeinclude
```

A user whose own config says "always copy `.env`" and who then works in a repo
whose `.wt.toml` adds `config/local.yml` gets both. With replace semantics the
repo would silently drop the `.env`. Excludes accumulate for the mirror-image
reason: a global "never copy `*.pem`" has to hold against any repository's
config.

That arrow is **display order, not precedence**. It is the order `wt info` lists
patterns in, and a pattern set in more than one layer is listed once and credited
to the leftmost. Whether a file is copied never depends on which layer its
pattern came from — the unqualified list keys cannot conflict, so there is
nothing for a precedence rule to resolve.

`exclude` is always applied **last** and cannot be overridden — not by any
layer's `copy`, not by `copy_ignored`, and not by `link`. An excluded path is
reported as skipped rather than materialised.

`exclude` and `link` reject negated patterns outright, naming the pattern and the
layer it came from: a deny-list you can argue your way out of is not a deny-list,
whichever layer does the arguing. `!` remains available in `copy`, where it only
ever removes.

#### Removing an inherited pattern

You cannot un-add a pattern a lower layer contributed. Deny it instead, from
**any** layer:

```bash
git config --local --add wt.copy '!.env'   # quote it: shells expand a bare !
```

A deny beats every `copy` pattern and `copy_ignored` too, no matter which layer
either came from — this is the one place the layers interact, and it runs the
opposite way to precedence. So the "`.env` everywhere, except in this one repo"
case is answered from that repo's `.git/config`, with nothing committed.

`link` has no `!` form. The only way to stop an inherited link is to `exclude`
the path, which also stops it being copied and cannot be re-included from any
layer.

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
default `false`. The list keys are readable from git config too, as multi-valued
`wt.copy` / `wt.link` / `wt.exclude`, but they join the [union](#accumulation)
instead of a precedence chain.

Note the spelling: in git config it is `wt.copyIgnored`, not `wt.copy_ignored`
— see [Key spelling](#key-spelling). As with any git boolean the value may be
omitted to mean true:

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

`[hooks]` needs [approval](#hook-trust) wherever it came from, because it is
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
- Every hook needs your approval before it runs — your own config file's as well as a repository's committed `.wt.toml` — see [Hook trust](#hook-trust)
- Set `WT_HOOKS_DISABLED=1` to skip all hooks (useful for scripting or CI)

## Hook trust

**`wt` runs no hook you have not approved.** Not the ones a repository ships in
its committed `.wt.toml`, and not the ones in your own config file either.

`.wt.toml` lives in the working tree, so it is normally committed and travels
with the repository — its `[hooks]` table is something the *repository* supplies
rather than something you wrote. Your own config file is something you wrote, but
"you wrote it" is a claim about a file on disk, and a file on disk is not
self-evidently yours: `~/.config/wt/config.toml` is as writable as anything else
in `$HOME`. Approving it once costs one prompt and removes the guess.

```console
$ wt create feat/x

⚠ These commands come from /home/you/src/acme/.wt.toml (not trusted):

  → [post_create] cd "$WT_PATH" && npm install
    [pre_remove] docker compose down

  → runs now; approving covers the rest too.

? Run these hooks?
  ▸ Skip these commands
    Run once
    Run, and remember this until the commands change
```

With no terminal to ask on — scripts, CI, `--format json` — the answer is
"skip" unless `WT_HOOKS_APPROVE_ALL=1` is set, and `wt` says so on stderr rather
than failing the command.

Approve ahead of time, or review what is approved:

```bash
wt trust             # approve everything that applies here
wt trust --list      # every approval on this machine
wt untrust           # revoke this repository's approval
wt untrust --global  # revoke your config file's approval
```

`wt trust` approves both sources that apply where you are standing — your config
file's hooks and, if the repository has a `.wt.toml`, that repository's — and
prints the commands first, because approving without reading is the failure this
whole mechanism exists to prevent. The two are recorded separately: approving
here does not widen the repository's reach, and does not pin your own hooks to
this checkout.

### What an approval covers

An approval is pinned to **the commands, and to the source that supplied them**:

- Your config file's hooks are approved once and run in every repository. That is
  what a *user* config is for.
- A repository's `.wt.toml` is approved for that repository only. An identical
  `.wt.toml` in a different repository is not covered: `make setup` is only as
  safe as the Makefile next to it.

Changing a hook command invalidates the approval and `wt` asks again — including
a `git pull` that adds a hook, or checking out a branch whose `.wt.toml` differs.
Changing anything *else* in the file does not: editing `pattern` or a `[files]`
entry is not a change to what would execute, so it does not cost you a prompt.

Approvals are stored in `~/.config/wt/trust.toml` (`$XDG_CONFIG_HOME/wt/` or
`%APPDATA%\wt\` if set). Deleting that file revokes everything.

That location is always an absolute path. A non-absolute `$XDG_CONFIG_HOME` (or
`%APPDATA%` on Windows) is invalid per the XDG Base Directory spec and is ignored
with a warning. If none of them yields an absolute directory and there is no home
directory either — an unset `HOME`, as happens in some CI and container images —
wt records nothing and treats every hook as unapproved. Resolving a relative path
against the working directory would put `trust.toml` *inside* whichever
repository is being gated, where a cloned repo could ship approvals for its own
hooks. If you hit this in CI, set `HOME` or an absolute `XDG_CONFIG_HOME`; to run
hooks there deliberately, see [Requiring approval for every
hook](#requiring-approval-for-every-hook) for `WT_HOOKS_APPROVE_ALL`.

An absolute setting is taken as given, including one that happens to point inside
a working tree — `XDG_CONFIG_HOME=/srv/repo/.config` keeps approvals in
`/srv/repo/.config/wt/trust.toml`. That is the same directory in every repository
you enter, so no repository can arrive at it by being cloned or entered; where
your own approvals live is your call. Worth knowing if your home directory is
itself a tracked repository: `trust.toml` is a local record, not something to
commit.

### Trusting paths ahead of time

If everything under a directory is yours, say so once instead of approving each
repository as you clone it:

```toml
[trust]
prefix = ["~/src/mine"]      # this directory and everything below it
exact  = ["~/src/acme/api"]  # this repository only
```

Hooks in a repository covered by a `[trust]` rule run without asking, and without
being recorded — there is nothing to invalidate, so a rule keeps applying as the
repository's hooks change. That is the trade: `prefix` is an assertion about a
directory you control, not about the commands under it. Point it at a tree you
clone other people's code into and you have turned the gate off for that tree.

`[trust]` is read from your config file only, never from a `.wt.toml` — a
repository that could whitelist itself would not be gated at all. Prefix matching
is on whole path segments, so `~/src/mine` does not cover
`~/src/mine-from-the-internet`. `wt untrust` cannot revoke a rule; edit the config
file (`wt untrust` says so when a rule still covers the repository).

A rule names its directory literally. `~` is expanded; environment variables are
not, and a rule containing `$` or `%` is ignored and reported. This is the one
place wt refuses to expand them, because an expansion that fails shortens a rule
instead of failing it: an unset `SRC` turns `$SRC/repos` into `/repos`, and
`$SRC/Users` into `/Users` — an existing directory holding every repository on
the machine. Rules naming a filesystem root are rejected for the same reason, as
are relative ones — a rule has to name one directory, not a different one
depending on where you ran `wt` from. Nothing else is adjusted either: an entry
that is only whitespace is skipped as the blank line it is, but a trailing space
inside a rule is part of the directory's name, since trimming `/srv/team ` to
`/srv/team` would hand it a wider tree than it names. On Windows the rule reaches
`C:\srv\team` regardless, because Windows itself strips trailing spaces from a
path component — as with case-insensitivity there, a rule covers the one
directory its path names on that machine. `wt trust --list` shows each rule next
to what it resolves to, or marks it ignored.

### Requiring approval for every hook

```toml
hooks_policy = "prompt-untrusted"   # default
```

| Value | Behaviour |
| --- | --- |
| `prompt-untrusted` | Anything not yet approved is shown and confirmed; approved hooks run |
| `prompt-all` | Every hook batch is shown and confirmed, however it was approved |
| `trusted-only` | Never prompts: approved hooks run, anything else is skipped |
| `off` | No hooks run at all (same as `WT_HOOKS_DISABLED=1`) |

`prompt-all` covers what a one-time approval does not: your own
`post_checkout = ["cd $WT_PATH && npm install"]` runs whatever lifecycle
scripts are in the `package.json` of whichever repository you happen to be
standing in, and that changes without your config file changing. It overrides
`[trust]` rules too — "ask me every time" has to mean every time.

`trusted-only` is the one to reach for in CI: it never blocks on a prompt, and it
never runs anything you did not approve on that machine.

Override per invocation with `WT_HOOKS_POLICY`. `hooks_policy` is read from
your config file only — never from a repo-level `.wt.toml`, since a repository
choosing how closely `wt` scrutinises that same repository's hooks would defeat
the point.

For automation you control, `WT_HOOKS_APPROVE_ALL=1` approves every batch without
asking: it skips the approval check, including the trust store, so do not export
it in your shell rc. It does not turn hooks *on* — `hooks_policy = "off"` and
`WT_HOOKS_DISABLED=1` still run nothing, since those say "no hooks" rather than
"ask me about hooks".

### Upgrading from wt 0.3 and earlier

Before 0.4, hooks from your own config file ran unprompted and only a repo's
`.wt.toml` was gated. Approvals from those versions pinned the *bytes of a file*
rather than a set of commands, so they cannot be translated into the new records
and are not carried over — `wt` drops them, says so on stderr, and asks once more
for each set of hooks you use. Answer "Run, and remember this until the commands
change", or run `wt trust` where you use them.

If you would rather not be asked at all for trees you own, `[trust]` above is the
escape hatch.

### Why hooks are not readable from git config

Most settings can come from `git config` at either scope. `[hooks]` cannot, at
any scope, and neither can `hooks_policy`. The dividing line is not scalar
versus list, or file format: **git config carries settings, never commands, nor
the policy that gates commands.**

For `[hooks]` the reason is mechanical. Nothing runs unprompted, so a new source
cannot land on a permissive side — but approvals are recorded per source, and a
source `wt` does not recognise has no scope to record one under. Hooks from
`.git/config` would therefore prompt on every single command, forever, which is
not a usable feature; and they are not the kind of thing worth making usable.
`.git/config` is not reliably something you wrote: it comes along with a
directory somebody hands you, an archive you unpack, or a restored backup. A
repository that arrives as a directory rather than as a clone brings its own
`.git/config` with it.

For `hooks_policy` the reason is the same one that keeps it out of a repo-level
`.wt.toml`: a repository choosing how closely `wt` scrutinises that
repository's hooks defeats the mechanism. `--global` git config would be safe
enough on its own, but a setting that means one thing in `~/.gitconfig` and is
silently ignored in `.git/config` is worse than one that is simply never read.

`[files]` is on the readable side of the line because it is declarative data,
bounded by [invariants F1–F7](#why-files-needs-no-trust-prompt) rather than by a
prompt. Its copy candidates are a subset of what `wt.copyIgnored` already
selects — and that key has been readable from `--local`, ungated, all along.

### Which shell runs a hook

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

Under `cmd /c`, `wt` skips the hooks for a worktree whose path, branch or repository
name contains `& | < > ^ "` or a line break, and says which one. `cmd` expands
`%WT_PATH%` *while* it parses, so those characters would be read as syntax and the
line above would run as two commands — and since none of them is a hook command,
an approval would not have covered the change. A `sh -c` hook is unaffected: the
shell substitutes `$WT_PATH` after parsing and never re-reads the result. If you
hit this, the usual cause is a branch name or a repository's `pattern`; rename it.

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

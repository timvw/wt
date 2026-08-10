# Examples

> **Note:** every hook on this page is written in POSIX shell syntax. That is what `wt` runs them with on macOS, Linux, and on Windows under Git Bash, MSYS2 or Cygwin. From a PowerShell or `cmd` session on Windows, hooks run through `cmd /c` instead and need cmd syntax (`%WT_PATH%`, not `$WT_PATH`) — see [Which shell runs a hook](configuration.md#which-shell-runs-a-hook).

## AI assistants and editors

Launch an AI coding assistant or editor automatically when checking out a worktree.

### Claude Code + tmux

![wt claude-tmux](wt-claude-tmux.gif)

```toml
[hooks]
post_create = [
  "tmux new-session -d -s \"$WT_REPO_NAME/$WT_BRANCH\" -c \"$WT_PATH\" \"claude -n '$WT_REPO_NAME/$WT_BRANCH'\" 2>/dev/null; echo \"tmux session: $WT_REPO_NAME/$WT_BRANCH\""
]
post_checkout = [
  "tmux new-session -d -s \"$WT_REPO_NAME/$WT_BRANCH\" -c \"$WT_PATH\" \"claude -n '$WT_REPO_NAME/$WT_BRANCH'\" 2>/dev/null; echo \"tmux session: $WT_REPO_NAME/$WT_BRANCH\""
]
pre_remove = [
  "tmux kill-session -t \"$WT_REPO_NAME/$WT_BRANCH\" 2>/dev/null || true"
]
```

Claude Code also has built-in worktree support via `claude --worktree --tmux`, but it uses a fixed directory layout. The hooks approach lets you keep `wt`'s configurable strategies and naming conventions.

### Other assistants and editors

```toml
[hooks]
# OpenAI Codex CLI in tmux
post_checkout = [
  "tmux new-session -d -s \"$WT_REPO_NAME/$WT_BRANCH\" -c \"$WT_PATH\" codex 2>/dev/null"
]

# Aider in tmux
# post_checkout = [
#   "tmux new-session -d -s \"$WT_REPO_NAME/$WT_BRANCH\" -c \"$WT_PATH\" aider 2>/dev/null"
# ]

# Open worktree in VS Code
# post_checkout = ["code $WT_PATH"]

# Open worktree in Cursor
# post_checkout = ["cursor $WT_PATH"]

# Open worktree in Zed
# post_checkout = ["zed $WT_PATH"]
```

Mix and match — for example, open VS Code _and_ start Claude Code in tmux:

```toml
[hooks]
post_checkout = [
  "code $WT_PATH",
  "tmux new-session -d -s \"$WT_REPO_NAME/$WT_BRANCH\" -c \"$WT_PATH\" \"claude -n '$WT_REPO_NAME/$WT_BRANCH'\" 2>/dev/null"
]
pre_remove = [
  "tmux kill-session -t \"$WT_REPO_NAME/$WT_BRANCH\" 2>/dev/null || true"
]
```

## Copy `.env` to new worktrees

Automatically copy environment files from the main worktree so new branches are ready to run immediately:

```toml
[hooks]
post_create = [
  "test -f $WT_MAIN/.env && cp $WT_MAIN/.env $WT_PATH/.env || true"
]
post_checkout = [
  "test -f $WT_MAIN/.env && cp $WT_MAIN/.env $WT_PATH/.env || true"
]
```

This copies `.env` only if it exists in the main checkout. Extend the pattern for other config files (`.env.local`, `config/local.yml`, etc.) by adding more `cp` commands.

## Task spanning multiple repositories

When a task or story requires changes across multiple repositories (e.g. a shared library and a main application), you can organize worktrees by feature instead of by repo using a custom pattern:

![wt multi-repo](wt-multi-repo.gif)

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

## Using environment variables in patterns

You can reference any environment variable in your pattern with `{.env.VARNAME}`. This lets you group worktrees by an external value such as a feature name, ticket ID, or sprint.

```toml
# ~/.config/wt/config.toml
strategy = "custom"
pattern = "{.worktreeRoot}/{.env.FEATURE}/{.repo.Name}"
```

```bash
export FEATURE=PROJ-42-new-checkout

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
```

If the referenced environment variable is not set, `wt` will return an error.

## Git submodules in worktrees

Git worktrees don't automatically initialize submodules. Use a hook to handle this:

```toml
[hooks]
post_create = ["cd $WT_PATH && git submodule update --init --recursive"]
post_checkout = ["cd $WT_PATH && git submodule update --init --recursive"]
```

This works well when all branches use the same (or similar) submodule commits — which is the common case.

> **Caveat:** All worktrees share a single `.git/modules/` directory. If two worktrees pin the **same submodule to different commits**, running `git submodule update` in one worktree will move the shared checkout, making the submodule appear dirty in the other worktree. This is a [known git limitation](https://git-scm.com/docs/git-worktree#_bugs), not specific to wt. If your branches frequently diverge on submodule versions, consider using separate clones (`git clone`) instead of worktrees for that repo.

## Deterministic dev server port per worktree

When running multiple worktrees simultaneously, each dev server needs a unique port. Use a post-checkout hook to compute a deterministic port offset from the branch name:

```toml
[hooks]
post_create = [
  "printf 'PORT=%d\\n' $(( 3000 + $(printf '%s' \"$WT_BRANCH\" | cksum | cut -d' ' -f1) % 997 )) > $WT_PATH/.env.port"
]
post_checkout = [
  "printf 'PORT=%d\\n' $(( 3000 + $(printf '%s' \"$WT_BRANCH\" | cksum | cut -d' ' -f1) % 997 )) > $WT_PATH/.env.port"
]
```

This hashes the branch name with `cksum` and maps it to a port in the range 3000–3996. The result is stable — the same branch always gets the same port.

Then read it in your dev server config (e.g. `vite.config.ts`):

```js
import { readFileSync } from 'fs';

const port = (() => {
  try {
    const env = readFileSync('.env.port', 'utf8');
    const match = env.match(/PORT=(\d+)/);
    return match ? parseInt(match[1]) : 3000;
  } catch { return 3000; }
})();

export default { server: { port } };
```

Or source it in a shell script:

```bash
source .env.port 2>/dev/null || PORT=3000
echo "Starting dev server on port $PORT"
```

## Shared build cache across worktrees

Each worktree normally gets its own `node_modules`, `.venv`, or build output directory. This wastes disk space and install time. Use a hook to symlink these from a shared per-repo cache:

```toml
[hooks]
# Node.js — share node_modules via a central cache per repo
post_create = [
  "mkdir -p $HOME/.cache/wt/$WT_REPO_NAME/node_modules && ln -sf $HOME/.cache/wt/$WT_REPO_NAME/node_modules $WT_PATH/node_modules && cd $WT_PATH && npm install"
]

# Python — share a single venv per repo
# post_create = [
#   "mkdir -p $HOME/.cache/wt/$WT_REPO_NAME/venv && ln -sf $HOME/.cache/wt/$WT_REPO_NAME/venv $WT_PATH/.venv && cd $WT_PATH && uv sync"
# ]

# Rust — share the target directory
# post_create = [
#   "mkdir -p $HOME/.cache/wt/$WT_REPO_NAME/target && ln -sf $HOME/.cache/wt/$WT_REPO_NAME/target $WT_PATH/target"
# ]

# Terraform — share the .terraform directory (providers, modules)
# post_create = [
#   "mkdir -p $HOME/.cache/wt/$WT_REPO_NAME/.terraform && ln -sf $HOME/.cache/wt/$WT_REPO_NAME/.terraform $WT_PATH/.terraform && cd $WT_PATH && terraform init"
# ]
```

All worktrees for the same repo point to one `node_modules` (or `.venv`, or `target/`). The first `npm install` populates the cache; subsequent worktrees reuse it instantly.

> **Note:** Shared mutable caches work well for dependencies that are branch-independent. If branches use different dependency versions, consider per-branch cache keys instead:
> ```bash
> mkdir -p $HOME/.cache/wt/$WT_REPO_NAME/$WT_BRANCH/node_modules
> ```

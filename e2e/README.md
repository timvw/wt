# E2E Tests

Declarative end-to-end tests for `wt` using YAML scenarios.

## Quick Start

```bash
# Run all e2e tests with auto-detected shells
just e2e

# Run with specific shell
just e2e-bash
just e2e-zsh

# Run with multiple shells
just e2e-shells bash,zsh
```

## Structure

```
e2e/
├── scenarios/          # YAML test definitions (all *.yaml here are auto-discovered)
│   ├── checkout.yaml
│   ├── clone.yaml      # wt clone: category placement, dest override, errors
│   ├── create.yaml
│   ├── hooks.yaml      # pre/post hooks, incl. pre_clone / post_clone
│   ├── list.yaml
│   ├── remove.yaml
│   ├── shellenv.yaml
│   └── ...             # and more
├── run.go              # Go orchestrator
└── README.md
```

## Adding Tests

Create or edit a YAML file in `scenarios/`:

```yaml
name: my-feature
description: Test my feature

scenarios:
  - name: basic_test
    description: Test basic functionality
    setup:
      - create_branch: test-branch
    steps:
      - run: wt checkout test-branch
        expect:
          cwd_ends_with: /test-branch
          branch: test-branch
          exit_code: 0
```

### Available Setup Steps

| Step | Example | Description |
|------|---------|-------------|
| `create_branch` | `create_branch: feature` | Create branch from main |
| `create_file` | `create_file: {path: foo.txt, content: "..."}` | Create file |
| `git_add` | `git_add: foo.txt` | Stage file |
| `git_commit` | `git_commit: "message"` | Commit staged changes |
| `git_checkout` | `git_checkout: main` | Switch branch |

### Available Expectations

| Expectation | Description |
|-------------|-------------|
| `exit_code` | Expected exit code (default: 0) |
| `cwd_ends_with` | Current directory ends with path |
| `branch` | Current git branch name |
| `output_contains` | Output includes string |
| `output_not_contains` | Output excludes string |

### Scenario Variables

POSIX scenarios (bash/zsh/fish) can reference these, set up by the generator:

| Variable | Form | Use |
|----------|------|-----|
| `$WT_BIN` | POSIX | The `wt` binary, invocable by the interpreter |
| `$TEST_DIR`, `$REPO_DIR` | POSIX | Paths to hand to `test`, `cat`, `mkdir`, … |
| `$WORKTREE_ROOT` | native | Exported; what `wt` itself reads |
| `$WORKTREE_ROOT_POSIX` | POSIX | Assert on worktree paths from the shell |
| `$TEST_DIR_NATIVE`, `$REPO_DIR_NATIVE` | native | Paths to hand to `wt` (e.g. `WT_CONFIG=`) |

The `_NATIVE`/`_POSIX` pairs only differ under Git Bash, where the interpreter
speaks `/c/...` and the native `wt.exe` speaks `C:\...`. Bash converts
POSIX-looking *arguments* when it spawns a native binary, but not *environment
variables* — so anything passed to `wt` through the environment needs the
native form. Everywhere else both forms are the same string, which is what
keeps a scenario using them portable.

These are POSIX-generator only; the PowerShell generator does not define them.

### Skip Conditions

```yaml
scenarios:
  - name: bash_only_test
    skip_shells: [zsh, powershell, pwsh]
    skip_os: [windows]
    # ...
```

## How It Works

1. `run.go` parses YAML scenarios
2. For each scenario × shell combination:
   - Generate shell script (POSIX or PowerShell)
   - Execute in subprocess
   - Check assertions
3. Report pass/fail summary

## CI Integration

The CI workflow runs `go run e2e/run.go` with the appropriate shell for each OS:

| OS | Shells |
|----|--------|
| Linux | bash, zsh, fish |
| macOS | bash, zsh, fish |
| Windows | powershell, pwsh, bash (Git Bash) |

On Windows, `bash` means Git Bash specifically — `run.go` resolves it from the
Git for Windows install rather than `PATH`, so the WSL launcher in `System32`
is never picked up. Git Bash runs the same native `wt.exe` as PowerShell but
through the bash integration, so scenarios there exercise the paths that need
`cygpath` translation and the missing-`script(1)` fallback. Scenarios that only
make sense there use `skip_os: [linux, darwin]`.

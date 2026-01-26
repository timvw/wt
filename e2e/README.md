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
├── scenarios/          # YAML test definitions
│   ├── checkout.yaml
│   ├── create.yaml
│   ├── list.yaml
│   ├── remove.yaml
│   └── shellenv.yaml
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
| Linux | bash, zsh |
| macOS | bash, zsh |
| Windows | powershell, pwsh |

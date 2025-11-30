# Native E2E Tests

This directory contains end-to-end tests for `wt` that run directly on the host system (no Docker required). These tests verify shell integration across bash and zsh.

## Overview

The E2E suite verifies that `wt` works correctly in different shell environments by:

1. Using the pre-built `wt` binary from PATH
2. Creating a temporary git repository with test branches
3. Loading the shell environment via `wt shellenv`
4. Running core operations: `checkout`, `create`, `list`, and `remove`
5. Verifying auto-cd functionality and worktree management

## Running Locally

### Prerequisites

- Go 1.23+ installed
- Git installed
- Shells to test (bash, zsh)
- From the repository root

### Build wt

```bash
go build -o wt .
```

### Run tests for a specific shell

```bash
# Add wt to PATH
export PATH="$(pwd):$PATH"

# Bash
bash e2e/native/test-bash.sh

# Zsh (if installed)
zsh e2e/native/test-zsh.sh
```

### Run all tests

```bash
./e2e/native/run-all.sh
```

## Test Structure

Each shell test script (`test-bash.sh`, `test-zsh.sh`) follows the same pattern:

1. **Setup**: Verify `wt` is in PATH and create temporary test repository
2. **Test 1**: Verify `wt checkout` with existing branch and auto-cd
3. **Test 2**: Verify `wt create` with new branch and auto-cd
4. **Test 3**: Verify `wt list` displays worktrees
5. **Test 4**: Verify `wt remove` deletes worktree
6. **Cleanup**: Remove temporary directories

## CI Integration

The tests run automatically in GitHub Actions on every push and pull request via the `e2e-linux.yml` workflow. The workflow:

- Uses `ubuntu-latest` runner
- Installs required shells (zsh)
- Builds `wt` binary
- Runs tests in parallel for bash and zsh
- Uploads test logs as artifacts on failure
- Keeps runtime under 2-3 minutes per shell

## Advantages over Docker approach

- **Faster**: No Docker build/pull time (~30-45s savings)
- **Simpler**: Direct test execution, fewer moving parts
- **More realistic**: Tests run in actual OS environment, not container
- **Easier to debug**: Direct access to test output, no container layers

## Supported Shells

- ✅ **bash**: Fully supported
- ✅ **zsh**: Fully supported
- ❌ **fish**: Not yet supported (wt shellenv doesn't generate fish syntax)

## Troubleshooting

If tests fail locally:

1. Ensure `wt` is built: `go build -o wt .`
2. Ensure `wt` is in PATH: `export PATH="$(pwd):$PATH"`
3. Verify git is configured: `git config --global user.email` and `user.name`
4. Check shell is available: `which bash` or `which zsh`
5. Run with verbose output: add `set -x` to the test script

If tests fail in CI:

1. Check the uploaded test logs in the Actions artifacts
2. Look for shell-specific issues in the shellenv output
3. Verify the runner has the expected shells installed

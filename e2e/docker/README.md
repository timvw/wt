# Docker-based E2E Tests (Local Development Only)

This directory contains Docker-based end-to-end tests for local development and debugging. **These tests do NOT run in CI** - the GitHub Actions workflow uses native runner tests instead (see `e2e/native/`).

## Overview

The Docker-based E2E suite verifies that `wt` works correctly in different shell environments by:

1. Building the `wt` binary from source inside a container
2. Creating a temporary git repository with test branches
3. Loading the shell environment via `wt shellenv`
4. Running core operations: `checkout`, `create`, `list`, and `remove`
5. Verifying auto-cd functionality and worktree management

**Note:** These Docker tests are provided for local development and debugging. CI uses the native runner approach (e2e/native/) which is faster and more realistic.

## Running Locally

### Prerequisites

- Docker installed and running
- From the repository root

### Build the test image

```bash
docker build -t wt-e2e -f e2e/docker/Dockerfile .
```

### Run tests for a specific shell

```bash
# Bash
docker run --rm -v $(pwd):/workspace -w /workspace wt-e2e bash e2e/docker/test-bash.sh

# Zsh
docker run --rm -v $(pwd):/workspace -w /workspace wt-e2e zsh e2e/docker/test-zsh.sh

# Fish (NOT YET SUPPORTED - wt shellenv doesn't generate fish syntax)
# docker run --rm -v $(pwd):/workspace -w /workspace wt-e2e fish e2e/docker/test-fish.sh
```

**Note:** Fish shell tests are included for future development but will currently fail since `wt shellenv` does not yet generate fish-compatible syntax.

### Run all tests

```bash
./e2e/docker/run-all.sh
```

## Test Structure

Each shell test script (`test-bash.sh`, `test-zsh.sh`, `test-fish.sh`) follows the same pattern:

1. **Setup**: Build `wt` binary and create temporary test repository
2. **Test 1**: Verify `wt checkout` with existing branch and auto-cd
3. **Test 2**: Verify `wt create` with new branch and auto-cd
4. **Test 3**: Verify `wt list` displays worktrees
5. **Test 4**: Verify `wt remove` deletes worktree
6. **Cleanup**: Remove temporary directories

## CI Integration

**These Docker tests do NOT run in CI.** GitHub Actions uses the native runner approach (`e2e/native/`) instead, which:

- Runs tests directly on ubuntu-latest (no Docker overhead)
- Executes existing Go e2e tests + shellenv validation + CRUD tests
- Completes in ~30-50 seconds per shell (vs 5-6 minutes with Docker)
- Follows the same pattern as macOS e2e tests (PR #8)

The Docker approach is provided as an alternative for local development and debugging.

## Troubleshooting

If tests fail locally:

1. Ensure Docker is running
2. Check that you're running from the repository root
3. Verify the Docker image builds successfully
4. Run with verbose output: add `set -x` to the test script

If tests fail in CI:

1. Check the uploaded test logs in the Actions artifacts
2. Look for shell-specific issues in the shellenv output
3. Verify git configuration is set correctly

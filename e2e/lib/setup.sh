#!/bin/bash
# Shared setup functions for e2e tests (bash/zsh)
# Sourced by the POSIX runner

set -eo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# Initialize test environment
# Sets up: TEST_DIR, REPO_DIR, REPO_NAME, WORKTREE_ROOT, WT_BIN
e2e_init() {
    local wt_binary="$1"

    if [[ -z "$wt_binary" ]]; then
        echo -e "${RED}ERROR: wt binary path required${NC}" >&2
        return 1
    fi

    if [[ ! -x "$wt_binary" ]]; then
        echo -e "${RED}ERROR: wt binary not found or not executable: $wt_binary${NC}" >&2
        return 1
    fi

    export WT_BIN="$wt_binary"
    export TEST_DIR=$(mktemp -d)
    export REPO_DIR="$TEST_DIR/test-repo"
    export REPO_NAME="test-repo"
    export WORKTREE_ROOT="$TEST_DIR/worktrees"

    mkdir -p "$REPO_DIR"
    mkdir -p "$WORKTREE_ROOT"

    # Initialize git repo
    cd "$REPO_DIR"
    git init --quiet
    git config user.email "test@example.com"
    git config user.name "Test User"
    git commit --allow-empty -m "initial commit" --quiet
    git branch -M main

    echo -e "${GREEN}Test environment initialized${NC}"
    echo "  TEST_DIR:      $TEST_DIR"
    echo "  REPO_DIR:      $REPO_DIR"
    echo "  WORKTREE_ROOT: $WORKTREE_ROOT"
    echo "  WT_BIN:        $WT_BIN"
}

# Cleanup test environment
e2e_cleanup() {
    if [[ -n "$TEST_DIR" && -d "$TEST_DIR" ]]; then
        rm -rf "$TEST_DIR"
        echo -e "${GREEN}Test environment cleaned up${NC}"
    fi
}

# Setup step: create a branch
# Usage: setup_create_branch <branch-name>
setup_create_branch() {
    local branch="$1"
    cd "$REPO_DIR"
    git checkout -b "$branch" --quiet
    git commit --allow-empty -m "commit on $branch" --quiet
    git checkout main --quiet
    echo "  Created branch: $branch"
}

# Setup step: create a file
# Usage: setup_create_file <path> <content>
setup_create_file() {
    local path="$1"
    local content="$2"
    cd "$REPO_DIR"
    echo "$content" > "$path"
    echo "  Created file: $path"
}

# Setup step: git add
# Usage: setup_git_add <path>
setup_git_add() {
    local path="$1"
    cd "$REPO_DIR"
    git add "$path"
    echo "  Staged: $path"
}

# Setup step: git commit
# Usage: setup_git_commit <message>
setup_git_commit() {
    local message="$1"
    cd "$REPO_DIR"
    git commit -m "$message" --quiet
    echo "  Committed: $message"
}

# Setup step: git checkout
# Usage: setup_git_checkout <branch>
setup_git_checkout() {
    local branch="$1"
    cd "$REPO_DIR"
    git checkout "$branch" --quiet
    echo "  Checked out: $branch"
}

# Reset to repo directory
# Usage: e2e_cd_repo
e2e_cd_repo() {
    cd "$REPO_DIR"
}

# Source wt shellenv
# Usage: e2e_source_shellenv
e2e_source_shellenv() {
    eval "$($WT_BIN shellenv)"
    echo "  Sourced shellenv"
}

# Assertion: check exit code
# Usage: assert_exit_code <expected> <actual>
assert_exit_code() {
    local expected="$1"
    local actual="$2"
    if [[ "$actual" -ne "$expected" ]]; then
        echo -e "${RED}FAIL: Expected exit code $expected, got $actual${NC}" >&2
        return 1
    fi
    return 0
}

# Assertion: check current working directory ends with
# Usage: assert_cwd_ends_with <suffix>
assert_cwd_ends_with() {
    local suffix="$1"
    local cwd=$(pwd)
    if [[ ! "$cwd" == *"$suffix" ]]; then
        echo -e "${RED}FAIL: Expected cwd to end with '$suffix', got '$cwd'${NC}" >&2
        return 1
    fi
    return 0
}

# Assertion: check current branch
# Usage: assert_branch <expected>
assert_branch() {
    local expected="$1"
    local actual=$(git branch --show-current)
    if [[ "$actual" != "$expected" ]]; then
        echo -e "${RED}FAIL: Expected branch '$expected', got '$actual'${NC}" >&2
        return 1
    fi
    return 0
}

# Assertion: check output contains string
# Usage: assert_output_contains <needle> <haystack>
assert_output_contains() {
    local needle="$1"
    local haystack="$2"
    if [[ ! "$haystack" == *"$needle"* ]]; then
        echo -e "${RED}FAIL: Expected output to contain '$needle'${NC}" >&2
        echo -e "${RED}Got: $haystack${NC}" >&2
        return 1
    fi
    return 0
}

# Assertion: check output does not contain string
# Usage: assert_output_not_contains <needle> <haystack>
assert_output_not_contains() {
    local needle="$1"
    local haystack="$2"
    if [[ "$haystack" == *"$needle"* ]]; then
        echo -e "${RED}FAIL: Expected output to NOT contain '$needle'${NC}" >&2
        echo -e "${RED}Got: $haystack${NC}" >&2
        return 1
    fi
    return 0
}

# Print pass message
e2e_pass() {
    local name="$1"
    echo -e "${GREEN}PASS${NC}: $name"
}

# Print fail message
e2e_fail() {
    local name="$1"
    local reason="$2"
    echo -e "${RED}FAIL${NC}: $name"
    if [[ -n "$reason" ]]; then
        echo "  Reason: $reason"
    fi
}

# Print skip message
e2e_skip() {
    local name="$1"
    local reason="$2"
    echo -e "${YELLOW}SKIP${NC}: $name"
    if [[ -n "$reason" ]]; then
        echo "  Reason: $reason"
    fi
}

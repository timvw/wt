#!/bin/zsh
setopt errexit
setopt pipefail

echo "=== Testing wt in zsh ==="

# Verify wt is available
if ! command -v wt &> /dev/null; then
    echo "✗ FAIL: wt command not found in PATH"
    exit 1
fi

echo "wt binary: $(which wt)"
echo "wt version: $(wt version 2>&1 || echo 'version command not available')"

# Create temporary git repo for testing
TEST_DIR=$(mktemp -d)
REPO_DIR="$TEST_DIR/test-repo"
export WORKTREE_ROOT="$TEST_DIR/worktrees"

echo "Setting up test repository at $REPO_DIR..."
mkdir -p "$REPO_DIR"
cd "$REPO_DIR"

# Initialize git repo with branches
git init
git config user.email "test@example.com"
git config user.name "Test User"
git commit --allow-empty -m "initial commit"
git branch -M main

# Create test branches
git checkout -b feature-branch
git commit --allow-empty -m "feature commit"
git checkout main

git checkout -b bugfix-branch
git commit --allow-empty -m "bugfix commit"
git checkout main

echo "Test repo setup complete. Branches:"
git branch

# Source shellenv
echo "Loading wt shellenv..."
eval "$(wt shellenv)"
echo "Shellenv loaded"

# Test 1: wt checkout (existing branch)
echo ""
echo "Test 1: wt checkout feature-branch"
wt checkout feature-branch
CURRENT_DIR=$(pwd)
EXPECTED_DIR="$WORKTREE_ROOT/test-repo/feature-branch"

if [[ "$CURRENT_DIR" == "$EXPECTED_DIR" ]]; then
    echo "✓ PASS: Auto-cd to worktree successful"
    echo "  Current dir: $CURRENT_DIR"
else
    echo "✗ FAIL: Auto-cd failed"
    echo "  Expected: $EXPECTED_DIR"
    echo "  Got: $CURRENT_DIR"
    exit 1
fi

# Verify we're on the right branch
CURRENT_BRANCH=$(git branch --show-current)
if [[ "$CURRENT_BRANCH" == "feature-branch" ]]; then
    echo "✓ PASS: On correct branch: $CURRENT_BRANCH"
else
    echo "✗ FAIL: Wrong branch. Expected: feature-branch, Got: $CURRENT_BRANCH"
    exit 1
fi

# Test 2: wt create (new branch)
cd "$REPO_DIR"
echo ""
echo "Test 2: wt create new-feature main"
wt create new-feature main
CURRENT_DIR=$(pwd)
EXPECTED_DIR="$WORKTREE_ROOT/test-repo/new-feature"

if [[ "$CURRENT_DIR" == "$EXPECTED_DIR" ]]; then
    echo "✓ PASS: Auto-cd to new worktree successful"
    echo "  Current dir: $CURRENT_DIR"
else
    echo "✗ FAIL: Auto-cd to new worktree failed"
    echo "  Expected: $EXPECTED_DIR"
    echo "  Got: $CURRENT_DIR"
    exit 1
fi

CURRENT_BRANCH=$(git branch --show-current)
if [[ "$CURRENT_BRANCH" == "new-feature" ]]; then
    echo "✓ PASS: On correct new branch: $CURRENT_BRANCH"
else
    echo "✗ FAIL: Wrong branch. Expected: new-feature, Got: $CURRENT_BRANCH"
    exit 1
fi

# Test 3: wt list
echo ""
echo "Test 3: wt list"
cd "$REPO_DIR"
wt list
echo "✓ PASS: wt list executed successfully"

# Test 4: wt remove
echo ""
echo "Test 4: wt remove feature-branch"
cd "$REPO_DIR"
wt remove feature-branch

if [[ ! -d "$WORKTREE_ROOT/test-repo/feature-branch" ]]; then
    echo "✓ PASS: Worktree directory removed"
else
    echo "✗ FAIL: Worktree directory still exists"
    exit 1
fi

# Cleanup
echo ""
echo "Cleaning up test directory..."
rm -rf "$TEST_DIR"

echo ""
echo "=== All zsh tests passed! ==="

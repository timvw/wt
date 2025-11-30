#!/bin/bash
set -euo pipefail

# Script to run all Docker-based e2e tests locally

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
IMAGE_NAME="wt-e2e:latest"

echo "=== Building Docker image for e2e tests ==="
docker build -t "$IMAGE_NAME" -f "$SCRIPT_DIR/Dockerfile" "$REPO_ROOT"

echo ""
echo "=== Running e2e tests in all shells ==="
echo "Note: Fish is excluded - wt shellenv doesn't support fish yet"
echo ""

SHELLS=(bash zsh)
FAILED=()

for shell in "${SHELLS[@]}"; do
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Running tests in $shell..."
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    if docker run --rm \
        -v "$REPO_ROOT:/workspace" \
        -w /workspace \
        "$IMAGE_NAME" \
        "$shell" "e2e/docker/test-$shell.sh"; then
        echo "✓ $shell tests passed"
    else
        echo "✗ $shell tests failed"
        FAILED+=("$shell")
    fi
done

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Summary"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ ${#FAILED[@]} -eq 0 ]; then
    echo "✓ All tests passed!"
    exit 0
else
    echo "✗ Tests failed in: ${FAILED[*]}"
    exit 1
fi

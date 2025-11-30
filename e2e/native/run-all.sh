#!/bin/bash
set -euo pipefail

# Script to run all native e2e tests locally

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "=== Building wt binary ==="
cd "$REPO_ROOT"
go build -o wt .
export PATH="$REPO_ROOT:$PATH"

echo "wt binary: $(which wt)"
echo ""

echo "=== Running e2e tests in all shells ==="

SHELLS=(bash)
FAILED=()

# Add zsh if available
if command -v zsh &> /dev/null; then
    SHELLS+=(zsh)
fi

for shell in "${SHELLS[@]}"; do
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Running tests in $shell..."
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    if "$shell" "$SCRIPT_DIR/test-$shell.sh"; then
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

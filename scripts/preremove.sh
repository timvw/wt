#!/bin/bash
# Pre-removal script for wt
# This script runs before the package is removed

# Don't fail the uninstall if cleanup fails
set +e

echo "Removing wt shell integration..."

# Try to remove shell integration for the user
# Use full path /usr/bin/wt for consistency with postinstall.sh
if [ -n "$SUDO_USER" ] && [ "$SUDO_USER" != "root" ]; then
    su - "$SUDO_USER" -c "/usr/bin/wt init --uninstall --no-prompt" 2>/dev/null
elif [ "$(id -u)" != "0" ]; then
    /usr/bin/wt init --uninstall --no-prompt 2>/dev/null
fi

# Always exit successfully - don't block uninstall if cleanup fails
exit 0

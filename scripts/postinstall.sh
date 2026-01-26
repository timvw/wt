#!/bin/bash
# Post-installation script for wt
# This script runs after the package is installed

set -e

echo "Configuring wt shell integration..."

# Try to run wt init for the installing user
# When installed via sudo, $SUDO_USER contains the original user
# Use full path /usr/bin/wt as PATH may not include it during package install
if [ -n "$SUDO_USER" ] && [ "$SUDO_USER" != "root" ]; then
    # Run as the original user, not as root
    if su - "$SUDO_USER" -c "/usr/bin/wt init --no-prompt" 2>/dev/null; then
        echo "Shell integration configured for user $SUDO_USER"
    else
        echo "Note: Could not auto-configure shell. Run 'wt init' manually."
    fi
elif [ "$(id -u)" != "0" ]; then
    # Not running as root, configure for current user
    if /usr/bin/wt init --no-prompt 2>/dev/null; then
        echo "Shell integration configured"
    else
        echo "Note: Could not auto-configure shell. Run 'wt init' manually."
    fi
else
    # Running as root without SUDO_USER - skip auto-configuration
    echo "Note: Run 'wt init' as your regular user to configure shell integration."
fi

echo ""
echo "wt has been installed successfully!"
echo ""
echo "To activate shell integration in your current session:"
echo "  source ~/.bashrc   # for bash"
echo "  source ~/.zshrc    # for zsh"
echo ""
echo "Or simply start a new terminal session."

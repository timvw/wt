#!/bin/bash
# Post-installation script for wt
# This script runs after the package is installed

set -e

echo "Configuring wt shell integration..."

# Determine the installing user's login shell so we can print the right
# "activate now" instructions below. When installed via sudo, $SHELL reflects
# root's shell, not the target user's, so look it up via getent instead.
if [ -n "$SUDO_USER" ] && [ "$SUDO_USER" != "root" ]; then
    TARGET_SHELL="$(getent passwd "$SUDO_USER" 2>/dev/null | cut -d: -f7)"
else
    TARGET_SHELL="$SHELL"
fi

# Try to run wt init for the installing user
# When installed via sudo, $SUDO_USER contains the original user
# Use full path /usr/bin/wt as PATH may not include it during package install
INIT_OK=0
if [ -n "$SUDO_USER" ] && [ "$SUDO_USER" != "root" ]; then
    # Run as the original user, not as root
    if su - "$SUDO_USER" -c "/usr/bin/wt init --no-prompt" 2>/dev/null; then
        echo "Shell integration configured for user $SUDO_USER"
        INIT_OK=1
    else
        echo "Note: Could not auto-configure shell. Run 'wt init' manually."
    fi
elif [ "$(id -u)" != "0" ]; then
    # Not running as root, configure for current user
    if /usr/bin/wt init --no-prompt 2>/dev/null; then
        echo "Shell integration configured"
        INIT_OK=1
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

if [ "$INIT_OK" = "1" ]; then
    echo "To activate shell integration in your current session:"
    case "$TARGET_SHELL" in
        */fish)
            echo "  source ~/.config/fish/config.fish   # for fish"
            ;;
        */zsh)
            echo "  source \"\${ZDOTDIR:-\$HOME}/.zshrc\"   # for zsh"
            ;;
        *)
            echo "  source ~/.bashrc   # for bash"
            ;;
    esac
    echo ""
    echo "Or simply start a new terminal session."
else
    echo "To enable shell integration manually, add one of these lines to the"
    echo "end of your shell config (~/.bashrc, ~/.zshrc, or"
    echo "~/.config/fish/config.fish), then restart your shell or source it:"
    echo ""
    echo "  eval \"\$(wt shellenv)\"          # bash/zsh"
    echo "  wt shellenv fish | source      # fish"
fi

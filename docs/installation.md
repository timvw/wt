# Installation

## Homebrew (macOS and Linux)

```bash
brew install timvw/tap/wt
wt init  # Configure shell integration
```

## Scoop (Windows)

```powershell
scoop bucket add timvw https://github.com/timvw/scoop-bucket
scoop install wt
wt init  # Configure shell integration
```

## WinGet (Windows)

```powershell
winget install timvw.wt
wt init  # Configure shell integration
```

## Nix

```bash
# Run without installing
nix run github:timvw/wt -- version

# Install to your profile
nix profile install github:timvw/wt
wt init  # Configure shell integration
```

For NixOS or home-manager, add to your configuration:

```nix
environment.systemPackages = [ pkgs.wt ];  # NixOS
# or
home.packages = [ pkgs.wt ];              # home-manager
```

## Linux Packages

Download `.deb`, `.rpm`, or `.pkg.tar.zst` packages from the [releases page](https://github.com/timvw/wt/releases).

```bash
# Debian/Ubuntu
sudo dpkg -i wt_*.deb

# Fedora/RHEL
sudo rpm -i wt_*.rpm

# Arch Linux (AUR)
yay -S wt-bin
```

Shell integration is automatically configured during package installation.

## From Source

```bash
go install github.com/timvw/wt@latest
wt init  # Configure shell integration
```

Or clone and build:

```bash
git clone https://github.com/timvw/wt.git
cd wt

# Using just (recommended)
just build            # builds to bin/wt
just install          # installs to /usr/local/bin (requires sudo)
just install-user     # installs to ~/bin (no sudo)

# Or build directly with go
mkdir -p bin
go build -o bin/wt .
sudo cp bin/wt /usr/local/bin/

# Configure shell integration
wt init
```

## Shell Integration

The `wt init` command automatically configures shell integration for your shell:

```bash
wt init              # Auto-detect shell and configure
wt init bash         # Configure for bash specifically
wt init zsh          # Configure for zsh specifically
wt init --dry-run    # Preview changes without modifying files
wt init --uninstall  # Remove wt configuration from shell
```

After running `wt init`, restart your shell or run:

```bash
source ~/.bashrc   # for bash
source "${ZDOTDIR:-$HOME}/.zshrc"    # for zsh
```

Shell integration enables:

- Automatic `cd` to worktree after `checkout`/`create`/`pr`/`mr` commands
- Tab completion for commands and branch names

**Manual setup** (alternative to `wt init`): Add this to the **END** of your shell config:

```bash
eval "$(wt shellenv)"
```

**Note for zsh users:** Place this after `compinit` in your config file.

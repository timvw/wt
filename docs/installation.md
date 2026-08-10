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

The installed `wt` binary bundles `git` in its runtime path. The shell
integration (`wt init`) runs in your interactive shell, however, and calls
`git` (completions) and `script(1)` (PTY for interactive commands) directly, so
make sure both are on your `PATH`: `git`, plus `util-linux` for `script(1)` on
Linux (on macOS `script` ships with the base system).

For NixOS or home-manager, add `wt` as a flake input and reference its package
in your modules (it is not part of `nixpkgs`, so `pkgs.wt` will not resolve):

```nix
# flake.nix
{
  inputs.wt.url = "github:timvw/wt";

  outputs = { self, nixpkgs, wt, ... }: {
    nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [
        ({ pkgs, ... }: {
          # NixOS
          environment.systemPackages = [ wt.packages.${pkgs.system}.default ];
          # home-manager equivalent:
          # home.packages = [ wt.packages.${pkgs.system}.default ];
        })
      ];
    };
  };
}
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
wt init fish         # Configure for fish specifically
wt init --dry-run    # Preview changes without modifying files
wt init --uninstall  # Remove wt configuration from shell
```

After running `wt init`, restart your shell or run:

```bash
source ~/.bashrc   # for bash
source "${ZDOTDIR:-$HOME}/.zshrc"    # for zsh
source ~/.config/fish/config.fish    # for fish
```

Shell integration enables:

- Automatic `cd` to worktree after `checkout`/`create`/`pr`/`mr` commands
- Tab completion for commands and branch names

**Manual setup** (alternative to `wt init`): Add this to the **END** of your shell config:

```bash
eval "$(wt shellenv bash)"   # use 'zsh' for zsh
```

For fish, add this instead:

```fish
wt shellenv fish | source
```

For PowerShell, add this to your `$PROFILE`:

```powershell
Invoke-Expression (& wt shellenv powershell)
```

Naming the shell explicitly is recommended. Without it, `shellenv` re-runs auto-detection
on every shell startup.

**Note for zsh users:** Place this after `compinit` in your config file.

### Windows: Git Bash vs PowerShell

`wt` is a native Windows binary, so it behaves the same whether you launch it from
PowerShell, Git Bash, or MSYS2 — but the shell integration differs. `wt init` detects
Git Bash and MSYS2 (via `MSYSTEM`/`SHELL`) and configures `~/.bashrc`; otherwise it
configures your PowerShell `$PROFILE`. Pass the shell explicitly to override:

```bash
wt init bash         # from Git Bash
wt init powershell   # from PowerShell
```

Git Bash does not ship `script(1)`, so the integration falls back to a non-PTY mode
there. Auto-`cd` and tab completion work; the interactive selection menus
(`wt checkout` with no arguments) need a TTY and are unavailable — pass a branch name
explicitly instead.

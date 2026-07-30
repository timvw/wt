{
  description = "wt - a fast, simple Git worktree manager";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        # Release binaries are versioned by goreleaser from the git tag; the flake
        # has no access to tags, so derive a self-describing version from the rev
        # instead of hardcoding a semver that silently goes stale after each release.
        version = self.shortRev or self.dirtyShortRev or "dev";
      in {
        packages = {
          wt = pkgs.buildGoModule {
            pname = "wt";
            inherit version;

            src = ./.;

            # To refresh after go.mod changes: set this to pkgs.lib.fakeHash, run
            # `nix build .#wt`, then copy the "got:" hash from the failure.
            vendorHash = "sha256-N72ExJZQZriohGVHZakkmqUqADBwoysz1jpmCQcVPVo=";

            # The repo has a second `package main` (./e2e), an internal test
            # orchestrator. Exclude it so only the `wt` binary is built/installed;
            # unlike `subPackages`, this keeps the full test suite in check scope.
            excludedPackages = [ "e2e" ];

            nativeBuildInputs = [ pkgs.makeWrapper ];

            # wt shells out to `git` at runtime; put it on the binary's PATH so the
            # package works even when git isn't otherwise installed. (Shell
            # integration additionally needs script(1) in the *user's* shell — see
            # docs/installation.md; a wrapper can't reach the interactive shell.)
            postInstall = ''
              wrapProgram $out/bin/wt \
                --prefix PATH : ${pkgs.lib.makeBinPath [ pkgs.git ]}
            '';

            nativeCheckInputs = with pkgs; [
              git
              bash
            ] ++ lib.optionals stdenv.isLinux [
              # provides script(1) for the wt shell function's PTY allocation;
              # Linux-only (util-linux is unavailable on Darwin, where script(1)
              # ships with the base system).
              util-linux
            ];

            preCheck = ''
              export HOME=$(mktemp -d)
              git config --global user.email "test@example.com"
              git config --global user.name "Test User"
              git config --global init.defaultBranch main
              # TestGetAvailableBranches and TestDefaultCmdRunsWithoutError call git in the
              # current directory; the Nix sandbox strips .git so we initialise one here.
              git init
              git add .
              git commit -m "initial"
            '';

            ldflags = [
              "-s" "-w"
              "-X github.com/timvw/wt/cmd.Version=${version}"
            ];

            meta = with pkgs.lib; {
              description = "A fast, simple Git worktree helper";
              homepage = "https://github.com/timvw/wt";
              license = licenses.mit;
              mainProgram = "wt";
            };
          };

          default = self.packages.${system}.wt;
        };

        apps = {
          wt = flake-utils.lib.mkApp { drv = self.packages.${system}.wt; };
          default = self.apps.${system}.wt;
        };
      });
}

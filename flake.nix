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
        version = "0.1.29";
      in {
        packages = {
          wt = pkgs.buildGoModule {
            pname = "wt";
            inherit version;

            src = ./.;

            vendorHash = "sha256-6FnhHVesWhG2AGY32YxgtHBUiGdJ7Kuuj4S/sqQBu0A=";

            nativeCheckInputs = with pkgs; [
              git
              bash
              util-linux  # provides script(1), used by the wt shell function to allocate a PTY
            ];

            # TestRemoveCleansUpResidualWorktreeDirectory writes a bash wrapper script at
            # runtime using #!/usr/bin/env bash, but /usr/bin/env is absent in the Nix
            # sandbox.  Patch the shebang to an absolute path instead.
            postPatch = ''
              substituteInPlace cmd/remove_cleanup_test.go \
                --replace-fail '#!/usr/bin/env bash' '#!${pkgs.bash}/bin/bash'
            '';

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

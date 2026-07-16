# Reproducible Linux build + test of the wt Nix flake — NOT a runtime image.
#
# Lets you validate `nix build .#wt` (and its sandboxed Go test suite) on Linux
# without installing Nix locally:
#
#   docker build -t wt-nix .
#
# The build runs the full flake — including the sandboxed `go test ./...` check
# phase — and fails if anything regresses. `path:` is used instead of a git ref
# so it builds the working tree as-is (version falls back to "dev").
# Pinned by digest for reproducibility (nixos/nix:latest as of this commit).
FROM nixos/nix@sha256:377d4887aca98f0dfa12971c1ea6d6a625a435d8b610d4c95a436843da6fbfd1

RUN echo "experimental-features = nix-command flakes" >> /etc/nix/nix.conf

WORKDIR /src
COPY . .

RUN nix build path:/src#wt -L \
    && ./result/bin/wt version \
    && ls -l result/bin/

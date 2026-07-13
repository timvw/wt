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
FROM nixos/nix@sha256:e64644d9e86a9b0e5033d00dd32bda36e6aa930e1b582bbee9e9f0e41f1bfe4a

RUN echo "experimental-features = nix-command flakes" >> /etc/nix/nix.conf

WORKDIR /src
COPY . .

RUN nix build path:/src#wt -L \
    && ./result/bin/wt version \
    && ls -l result/bin/

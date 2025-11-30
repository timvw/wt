# Docker image for wt e2e tests with multiple shells
FROM golang:1.23-bookworm

# Install git and shells (bash is already present)
RUN apt-get update && apt-get install -y \
    git \
    zsh \
    fish \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user for realistic shell testing
RUN useradd -m -s /bin/bash testuser

# Set up git config for testuser
USER testuser
RUN git config --global user.email "test@example.com" && \
    git config --global user.name "Test User"

WORKDIR /workspace

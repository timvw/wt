FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    ca-certificates \
    tree \
    tmux \
    nodejs \
    npm \
    && rm -rf /var/lib/apt/lists/*

# Install Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Install wt
ARG WT_VERSION=0.1.28
ARG TARGETARCH
RUN curl -fsSL "https://github.com/timvw/wt/releases/download/v${WT_VERSION}/wt_${WT_VERSION}_linux_${TARGETARCH}.tar.gz" \
    | tar -xz -C /usr/local/bin wt

# Install starship prompt
RUN curl -fsSL https://starship.rs/install.sh | sh -s -- --yes

# Create a demo user
RUN useradd -m demo
USER demo
WORKDIR /home/demo

# Configure git with colored output
RUN git config --global user.name "demo" && \
    git config --global user.email "demo@example.com" && \
    git config --global init.defaultBranch main && \
    git config --global color.ui auto

# Configure starship — minimal prompt showing directory + git branch
RUN mkdir -p .config && printf '\
format = "$directory$git_branch$git_status$character"\n\
[directory]\n\
truncation_length = 2\n\
style = "bold cyan"\n\
[git_branch]\n\
format = "on [$branch]($style) "\n\
style = "bold purple"\n\
[git_status]\n\
style = "bold red"\n\
[character]\n\
success_symbol = "[❯](bold green)"\n\
error_symbol = "[❯](bold red)"\n\
' > .config/starship.toml

# Bake starship init into .bashrc so it activates even with bare bash
RUN printf 'eval "$(starship init bash)"\neval "$(wt shellenv)"\n' > .bashrc

# Create main-app repo with branches and .env
RUN mkdir -p src/main-app && cd src/main-app && git init && \
    echo "APP_SECRET=s3cret" > .env && \
    echo "DB_HOST=localhost" >> .env && \
    git add .env && git commit -m "initial commit" && \
    git checkout -b feat/auth && git commit --allow-empty -m "add auth" && \
    git checkout -b feat/dashboard && git commit --allow-empty -m "add dashboard" && \
    git checkout -b fix/login-bug && git commit --allow-empty -m "fix login" && \
    git checkout main

# Create shared-lib repo with branches
RUN mkdir -p src/shared-lib && cd src/shared-lib && git init && \
    git commit --allow-empty -m "initial commit" && \
    git checkout -b feat/auth && git commit --allow-empty -m "add auth types" && \
    git checkout main

# Pre-bake config variants (selected via WT_CONFIG env var)
RUN mkdir -p .config/wt/variants

# Default config (global strategy)
RUN printf 'root = "~/worktrees"\n' > .config/wt/variants/default.toml

# Multi-repo config (group by branch)
RUN printf 'strategy = "custom"\npattern = "{.worktreeRoot}/{.branch}/{.repo.Name}"\n' \
    > .config/wt/variants/multi-repo.toml

# Hooks config (copy .env on create/checkout)
COPY --chown=demo:demo hooks.toml .config/wt/variants/hooks.toml

# Claude Code + tmux hooks config
COPY --chown=demo:demo claude-tmux.toml .config/wt/variants/claude-tmux.toml

# Copy default config
RUN cp .config/wt/variants/default.toml .config/wt/config.toml

ENV WORKTREE_ROOT=/home/demo/worktrees
ENV TERM=xterm-256color

WORKDIR /home/demo
CMD ["bash", "--rcfile", "/home/demo/.bashrc"]

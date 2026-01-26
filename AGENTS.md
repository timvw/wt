# Guidelines for AI Coding Agents

This document provides guidelines for AI coding agents (like Claude Code, Cursor, GitHub Copilot, etc.) working on the `wt` project.

## Golden Rule: Use Worktrees!

**IMPORTANT:** As an AI agent, you should practice what this tool preaches - use worktrees for your work!

### ❌ DON'T: Switch branches in the main repository
```bash
# BAD - Don't do this!
cd /path/to/main/wt/repo
git checkout -b feature/new-branch
```

This leaves the user's main working directory on a feature branch.

### ✅ DO: Create worktrees for each task
```bash
# GOOD - Use wt to create isolated worktrees!
wt create feature/new-branch
# This creates: ~/dev/worktrees/wt/feature/new-branch
# Main repo stays on main branch
```

## Agent Workflow

### 1. Starting New Work

When beginning a new feature or fix:

```bash
# Create a worktree for your feature
wt create feat/your-feature-name

# The worktree is automatically checked out to:
# ~/dev/worktrees/wt/feat/your-feature-name
```

### 2. Working in the Worktree

All file operations (Read, Write, Edit) should use paths in the worktree:

```bash
# Work in the worktree location
/Users/username/dev/worktrees/wt/feat/your-feature-name/

# NOT in the main repo:
# /Users/username/src/github/wt/  ❌
```

### 3. Committing and Creating PRs

```bash
# From within the worktree directory
git add .
git commit -m "feat: your feature description"
git push -u origin feat/your-feature-name

# Create PR
gh pr create --title "feat: your feature" --body "Description"
```

### 4. Cleaning Up

After the PR is merged:

```bash
# Remove the worktree
wt remove feat/your-feature-name
# or use the short alias:
wt rm feat/your-feature-name
```

## Why This Matters for Agents

### Benefits of Worktree-based Workflow

1. **Non-disruptive:** User's main repository stays on `main` branch
2. **Isolated:** Each feature gets its own directory
3. **Parallel work:** Can work on multiple features simultaneously
4. **Clean state:** No branch switching confusion
5. **Predictable:** Always know where files are located

### Common Pitfalls to Avoid

❌ **Checking out branches in main repo**
```bash
# This disrupts the user's workspace
cd /path/to/main/repo
git checkout feature-branch
```

❌ **Forgetting where you are**
```bash
# Always be aware of your current location
# Are you in main repo or a worktree?
```

❌ **Mixing work across locations**
```bash
# Don't edit files in main repo while working on a worktree feature
```

✅ **Use wt commands consistently**
```bash
# Create worktrees
wt create feat/new-feature

# Check existing worktrees
wt list

# Remove when done
wt rm feat/new-feature
```

## Interactive Commands

When run without arguments, several commands provide interactive selection:

```bash
# Interactively select a branch to checkout
wt checkout

# Interactively select a worktree to remove
wt remove

# Interactively select a PR to work on (requires gh CLI)
wt pr

# Interactively select an MR to work on (requires glab CLI)
wt mr
```

## Working with PRs and MRs

### GitHub Pull Requests
```bash
# List and select from open PRs
wt pr              # Interactive selection

# Or specify PR number/URL directly
wt pr 123
wt pr https://github.com/owner/repo/pull/123
```

This creates a worktree at `~/dev/worktrees/wt/pr-123`

### GitLab Merge Requests
```bash
# List and select from open MRs
wt mr              # Interactive selection

# Or specify MR number/URL directly
wt mr 456
wt mr https://gitlab.com/owner/repo/-/merge_requests/456
```

This creates a worktree at `~/dev/worktrees/wt/mr-456`

## Project-Specific Guidelines

### Branch Naming
Follow the project's convention:
- `feat/description` - New features
- `fix/description` - Bug fixes
- `docs/description` - Documentation
- `refactor/description` - Refactoring
- `chore/description` - Maintenance

### Commit Messages
Use [Conventional Commits](https://www.conventionalcommits.org/):
- `feat: add interactive selection`
- `fix: handle edge case in branch filtering`
- `docs: update agent guidelines`

### Before Creating PR

1. ✅ Ensure all tests pass: `go test ./...`
2. ✅ Run E2E tests: `go run e2e/run.go` (see Testing section below)
3. ✅ Run linter: `golangci-lint run` (or let CI do it)
4. ✅ Build succeeds: `go build -o bin/wt .`
5. ✅ Commit messages follow convention

## Testing

### Unit Tests

Standard Go tests in `*_test.go` files:

```bash
go test ./...
```

### E2E Tests (YAML-based)

This project uses a **declarative YAML-based E2E test framework** in `e2e/scenarios/`. Each command has its own YAML file (e.g., `checkout.yaml`, `create.yaml`, `cleanup.yaml`).

**Running E2E tests:**
```bash
# Build first, then run E2E tests
go build -o bin/wt .
go run e2e/run.go

# With verbose output
go run e2e/run.go -verbose
```

**Adding new E2E tests:**

Create or edit a YAML file in `e2e/scenarios/`:

```yaml
name: my-command
description: Test my-command functionality

scenarios:
  - name: basic_test
    description: Test basic functionality
    steps:
      - run: wt my-command arg
        expect:
          exit_code: 0
          output_contains: "expected output"
```

**Available setup steps** (defined in `e2e/run.go`):
- `create_branch: branch-name` - Creates a branch with a commit
- `create_file: {path: file.txt, content: "..."}` - Creates a file
- `git_add: file.txt` - Stages a file
- `git_commit: "message"` - Commits staged changes
- `git_checkout: branch` - Switches branch

**Available step actions:**
- `run: command` - Execute a shell command
- `cd: $REPO_DIR` - Change directory (use `$REPO_DIR` for test repo root)

**Available expectations:**
- `exit_code: 0` - Expected exit code
- `output_contains: "text"` - Output includes string
- `output_not_contains: "text"` - Output excludes string
- `cwd_ends_with: /path` - Current directory ends with path
- `branch: branch-name` - Current git branch

**Skip conditions:**
```yaml
scenarios:
  - name: unix_only_test
    skip_os: [windows]
    skip_shells: [powershell, pwsh]
    skip_shellenv: true  # Skip shell integration wrapper
```

**Important notes for E2E tests:**
- The framework creates a fresh git repo for each scenario
- Use `run:` steps for git operations not covered by setup (e.g., `git merge`)
- Tests run with both bash and zsh on Unix, PowerShell on Windows
- The `$WT_BIN` variable points to the built binary
- The `$REPO_DIR`, `$REPO_NAME`, and `$WORKTREE_ROOT` variables are available

## Shell Integration Note

The `wt` tool has shell integration that auto-navigates to worktrees:

```bash
# If user has shell integration enabled
wt create my-feature
# Shell automatically cd's to the worktree!
```

However, as an agent executing commands, you need to explicitly use the worktree paths in subsequent file operations.

## Example Agent Session

Here's a complete example of an agent working on a feature:

```bash
# 1. Create worktree for new feature
wt create feat/add-awesome-feature
# Creates: ~/dev/worktrees/wt/feat/add-awesome-feature

# 2. Work in that directory
# All file operations use:
# /Users/username/dev/worktrees/wt/feat/add-awesome-feature/

# 3. Make changes (Read, Write, Edit tools)
# Edit /Users/username/dev/worktrees/wt/feat/add-awesome-feature/main.go

# 4. Run tests in worktree
cd /Users/username/dev/worktrees/wt/feat/add-awesome-feature
go test ./...

# 5. Commit and push
git add .
git commit -m "feat: add awesome feature"
git push -u origin feat/add-awesome-feature

# 6. Create PR
gh pr create --title "feat: add awesome feature" \
  --body "This adds an awesome feature that does X, Y, Z"

# 7. After merge, clean up
wt rm feat/add-awesome-feature
```

## Questions?

If you have questions about using `wt` as an agent:
1. Read the main [README.md](README.md)
2. Check [CONTRIBUTING.md](CONTRIBUTING.md) for workflow details
3. Review examples in the documentation

## Summary for Agents

**Key Principle:** Use `wt` to create isolated worktrees for each task. Never switch branches in the user's main repository directory.

This keeps the user's workspace clean and demonstrates best practices for the very tool you're helping to build!

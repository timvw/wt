# Contributing to wt

## Development Workflow

### For External Contributors (Fork-based workflow)

If you don't have write access to the repository:

1. **Fork the repository** on GitHub

2. **Clone your fork:**
   ```bash
   git clone https://github.com/YOUR-USERNAME/wt.git
   cd wt
   ```

3. **Create a feature branch:**
   ```bash
   git checkout -b feature/your-feature-name
   ```

4. **Make your changes and commit:**
   ```bash
   git add .
   git commit -m "feat: your feature description"
   ```

5. **Push to your fork:**
   ```bash
   git push -u origin feature/your-feature-name
   ```

6. **Create a Pull Request** from your fork to `timvw/wt:main`
   - Go to https://github.com/timvw/wt
   - Click "New Pull Request"
   - Select "compare across forks"
   - Choose your fork and branch

7. **Wait for CI to pass** and respond to any review feedback

### For Maintainers (Branch-based workflow)

If you have write access to the repository:

1. **Create a feature branch:**
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes and commit:**
   ```bash
   git add .
   git commit -m "feat: your feature description"
   ```

3. **Push the branch:**
   ```bash
   git push -u origin feature/your-feature-name
   ```

4. **Create a Pull Request:**
   ```bash
   gh pr create --title "feat: your feature" --body "Description of changes"
   ```

5. **Wait for CI to pass** - Branch protection requires all checks to pass

6. **Merge the PR** when CI is green

### Branch Naming Convention

- `feat/description` - New features
- `fix/description` - Bug fixes
- `docs/description` - Documentation changes
- `refactor/description` - Code refactoring
- `chore/description` - Maintenance tasks

### Commit Message Convention

Follow [Conventional Commits](https://www.conventionalcommits.org/):

- `feat: add interactive selection for checkout`
- `fix: filter out invalid branch names`
- `docs: update installation instructions`
- `refactor: simplify branch filtering logic`
- `chore: update dependencies`
- `security: update vulnerable dependency`

### Running Tests Locally

Before pushing:

```bash
# Run tests
go test ./...

# Run linter
golangci-lint run

# Build
go build -o bin/wt .
```

### Branch Protection

The `main` branch is protected and requires:
- ✅ All CI checks must pass (Test, Build, Lint, Cross Compile)
- ✅ Branch must be up to date with main
- ❌ No direct pushes to main

## CI/CD

### Continuous Integration

Every push triggers:
- Tests on Go 1.21, 1.22, 1.23
- Linting with golangci-lint
- Build verification
- Cross-compilation checks

### Release Process

1. All changes merged to `main` via PRs
2. When ready to release:
   ```bash
   git tag v0.1.x
   git push origin v0.1.x
   ```
3. Automated workflow:
   - Builds binaries for all platforms
   - Creates Homebrew bottles
   - Publishes GitHub release
   - Updates Homebrew formula automatically

## Getting Help

- Check existing issues: https://github.com/timvw/wt/issues
- Read the README: https://github.com/timvw/wt#readme
- Ask questions in discussions

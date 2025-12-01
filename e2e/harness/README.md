# E2E Test Harness

This package provides a shared E2E test harness for testing `wt` across multiple shells and operating systems.

## Architecture

The harness follows a modular design with platform-agnostic test scenarios that run through shell-specific adapters:

```
┌─────────────────────────────┐
│  Generic Test Scenarios     │  ← One set of tests for all platforms
│  - Checkout with auto-cd    │
│  - Create new branch        │
│  - Remove worktree          │
│  - List worktrees           │
└──────────┬──────────────────┘
           │
┌──────────▼──────────────────┐
│   Test Runner               │  ← Orchestrates execution
│   - Setup fixtures          │
│   - Execute steps           │
│   - Run assertions          │
└──────────┬──────────────────┘
           │
┌──────────▼──────────────────┐
│   Shell Adapters            │  ← Platform-specific implementations
│   - BashAdapter             │
│   - ZshAdapter              │
│   - PowerShellAdapter       │
└─────────────────────────────┘
```

## Components

### Fixture (`fixture.go`)

Creates temporary git repositories for testing:

```go
fixture, err := NewFixture(t, wtBinary)
fixture.CreateBranch("feature", "main")
fixture.CreatePRRef(123, "feature")  // GitHub PR refs
fixture.CreateMRRef(456, "feature")  // GitLab MR refs
```

### Scenario (`scenario.go`)

Defines test scenarios with steps and assertions:

```go
scenario := Scenario{
    Name: "checkout with auto-cd",
    Setup: func(f *Fixture) error {
        return f.CreateBranch("feature", "main")
    },
    Steps: []Step{
        {Cmd: "wt", Args: []string{"checkout", "feature"}},
    },
    Verify: []Assertion{
        AssertExitCode(0),
        AssertPwdEquals("$WORKTREE_ROOT/$REPO/feature"),
        AssertStdoutContains("TREE_ME_CD:"),
    },
}
```

### Adapter (`adapter.go`)

Interface for shell-specific execution:

```go
type ShellAdapter interface {
    Name() string
    Setup(wtBinary, worktreeRoot, repoDir string) error
    Execute(cmd string, args []string) (*Result, error)
    GetPwd() (string, error)
    Cleanup() error
}
```

Implementations:
- `bash_adapter.go` - Bash shell (Linux/macOS) ✅
- `zsh_adapter.go` - Zsh shell (Linux/macOS) ✅
- `pwsh_adapter.go` - PowerShell (Windows) ✅

### Runner (`runner.go`)

Executes scenarios through adapters:

```go
runner, err := NewRunner(t, bashAdapter)
defer runner.Cleanup()

err = runner.Run(scenario)
```

## Usage

### Writing a Test Scenario

```go
func TestCheckoutExistingBranch(t *testing.T) {
    adapter := NewBashAdapter()  // or ZshAdapter, PwshAdapter
    runner, err := NewRunner(t, adapter)
    if err != nil {
        t.Fatal(err)
    }
    defer runner.Cleanup()

    scenario := Scenario{
        Name: "checkout existing branch",
        Setup: func(f *Fixture) error {
            return f.CreateBranch("feature-123", "main")
        },
        Steps: []Step{
            {Cmd: "wt", Args: []string{"checkout", "feature-123"}},
        },
        Verify: []Assertion{
            AssertExitCode(0),
            AssertPwdContains("feature-123"),
        },
    }

    if err := runner.Run(scenario); err != nil {
        t.Fatal(err)
    }
}
```

### Available Assertions

- `AssertExitCode(int)` - Verify command exit code
- `AssertStdoutContains(string)` - Check stdout contains text
- `AssertStderrContains(string)` - Check stderr contains text
- `AssertPwdEquals(string)` - Verify current directory (supports variables)
- `AssertPwdContains(string)` - Check pwd contains substring

### Variable Expansion

Assertions support variable expansion:

- `$WORKTREE_ROOT` - Worktree root directory
- `$REPO` - Repository name (e.g., "test-repo")
- `$REPO_DIR` - Full path to repository

Example:
```go
AssertPwdEquals("$WORKTREE_ROOT/$REPO/feature-branch")
```

## Environment Variables

- `WT_BINARY` - Path to wt binary (optional, will build from source if not set)

## Running Tests

```bash
# Run harness tests
go test ./e2e/harness/

# Run with specific binary
WT_BINARY=./wt go test ./e2e/harness/
```

## Status

**Current State:** Core framework and adapters complete
- ✅ Fixture builder
- ✅ Scenario definitions
- ✅ Adapter interface
- ✅ Test runner
- ✅ Shell adapters (bash ✅, zsh ✅, pwsh ✅)
- ⏳ Scenario migrations (next task: bez-8)
- ⏳ CI integration (task: bez-35)

## Design Goals

1. **Write once, run everywhere** - Same test scenarios work on Linux/macOS/Windows
2. **Shell isolation** - Tests run in real shell environments with shellenv loaded
3. **Fast feedback** - Parallel execution across shells
4. **Clear failures** - Detailed assertion errors with context
5. **Easy extension** - Add new shells by implementing adapter interface

// E2E test orchestrator for wt
// Reads YAML scenario files and executes them across shells
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scenario file structure
type ScenarioFile struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Scenarios   []Scenario `yaml:"scenarios"`
}

// Individual test scenario
type Scenario struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	Setup        []Setup  `yaml:"setup"`
	Steps        []Step   `yaml:"steps"`
	SkipShells   []string `yaml:"skip_shells"`
	SkipOS       []string `yaml:"skip_os"`
	SkipShellenv bool     `yaml:"skip_shellenv"`
	Interactive  bool     `yaml:"interactive"`
}

// Setup step (branch creation, file creation, etc.)
type Setup struct {
	CreateBranch string    `yaml:"create_branch"`
	CreateFile   *FileSpec `yaml:"create_file"`
	GitAdd       string    `yaml:"git_add"`
	GitCommit    string    `yaml:"git_commit"`
	GitCheckout  string    `yaml:"git_checkout"`
}

// File specification for create_file setup
type FileSpec struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
}

// Test step (command + expectations)
type Step struct {
	Run    string            `yaml:"run"`
	Cd     string            `yaml:"cd"`
	Env    map[string]string `yaml:"env"`
	Expect *Expect           `yaml:"expect"`
}

// Expectations for a step
type Expect struct {
	ExitCode          *int   `yaml:"exit_code"`
	CwdEndsWith       string `yaml:"cwd_ends_with"`
	Branch            string `yaml:"branch"`
	OutputContains    string `yaml:"output_contains"`
	OutputNotContains string `yaml:"output_not_contains"`
}

// Test result
type Result struct {
	Scenario string
	Shell    string
	Passed   bool
	Error    string
	Output   string
}

func main() {
	// Parse flags
	shellsFlag := flag.String("shells", "", "Comma-separated list of shells to test (bash,zsh,powershell,pwsh)")
	scenariosDir := flag.String("scenarios", "e2e/scenarios", "Directory containing scenario YAML files")
	wtBinary := flag.String("wt", "", "Path to wt binary (default: auto-detect)")
	verbose := flag.Bool("verbose", false, "Verbose output")
	showOutput := flag.Bool("show-output", false, "Print scenario output for each run")
	keepTmp := flag.Bool("keep-tmp", false, "Keep temporary directories created during tests")
	flag.Parse()

	// Determine shells to test
	shells := determineShells(*shellsFlag)
	if len(shells) == 0 {
		fmt.Println("No shells available to test")
		os.Exit(1)
	}

	// Find wt binary (must be absolute path)
	binary := findWtBinary(*wtBinary)
	if binary == "" {
		fmt.Println("ERROR: Could not find wt binary")
		os.Exit(1)
	}
	// Ensure absolute path
	if !filepath.IsAbs(binary) {
		abs, err := filepath.Abs(binary)
		if err != nil {
			fmt.Printf("ERROR: Could not get absolute path for binary: %v\n", err)
			os.Exit(1)
		}
		binary = abs
	}
	fmt.Printf("Using wt binary: %s\n", binary)

	// Load scenarios
	scenarios, err := loadScenarios(*scenariosDir)
	if err != nil {
		fmt.Printf("ERROR: Failed to load scenarios: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Loaded %d scenario files\n", len(scenarios))

	// Run tests
	passed, failed, skipped := 0, 0, 0

	for _, shell := range shells {
		fmt.Printf("\n=== Testing with %s ===\n", shell)

		for _, file := range scenarios {
			for _, scenario := range file.Scenarios {
				// Check skip conditions
				if shouldSkip(scenario, shell) {
					if *verbose {
						fmt.Printf("SKIP: %s/%s (shell: %s)\n", file.Name, scenario.Name, shell)
					}
					skipped++
					continue
				}

				// Run scenario
				result := runScenario(binary, shell, file.Name, scenario, *verbose, *showOutput, *keepTmp)

				if result.Passed {
					fmt.Printf("PASS: %s/%s\n", file.Name, scenario.Name)
					if *verbose && result.Output != "" {
						fmt.Printf("  Output: %s\n", result.Output)
					}
					if *showOutput {
						printScenarioOutput(result.Output)
					}
					passed++
				} else {
					fmt.Printf("FAIL: %s/%s\n", file.Name, scenario.Name)
					if result.Error != "" {
						fmt.Printf("  Error: %s\n", result.Error)
					}
					if *verbose && result.Output != "" {
						fmt.Printf("  Output: %s\n", result.Output)
					}
					if *showOutput {
						printScenarioOutput(result.Output)
					}
					failed++
				}
			}
		}
	}

	// Summary
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Passed:  %d\n", passed)
	fmt.Printf("Failed:  %d\n", failed)
	fmt.Printf("Skipped: %d\n", skipped)

	if failed > 0 {
		os.Exit(1)
	}
}

func printScenarioOutput(output string) {
	if output == "" {
		fmt.Println("  Output: (none)")
		return
	}
	fmt.Println("  Output:")
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			continue
		}
		fmt.Printf("    %s\n", line)
	}
}

func determineShells(shellsFlag string) []string {
	if shellsFlag != "" {
		return strings.Split(shellsFlag, ",")
	}

	// Auto-detect available shells
	var shells []string
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("powershell"); err == nil {
			shells = append(shells, "powershell")
		}
		if _, err := exec.LookPath("pwsh"); err == nil {
			shells = append(shells, "pwsh")
		}
	} else {
		if _, err := exec.LookPath("bash"); err == nil {
			shells = append(shells, "bash")
		}
		if _, err := exec.LookPath("zsh"); err == nil {
			shells = append(shells, "zsh")
		}
		if _, err := exec.LookPath("fish"); err == nil {
			shells = append(shells, "fish")
		}
	}
	return shells
}

func findWtBinary(specified string) string {
	if specified != "" {
		if _, err := os.Stat(specified); err == nil {
			return specified
		}
		return ""
	}

	// Try common locations
	candidates := []string{
		"./bin/wt",
		"./wt",
		"bin/wt",
	}
	if runtime.GOOS == "windows" {
		candidates = []string{
			"./bin/wt.exe",
			"./wt.exe",
			"bin/wt.exe",
		}
	}

	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}

	// Try PATH
	if path, err := exec.LookPath("wt"); err == nil {
		return path
	}

	return ""
}

func loadScenarios(dir string) ([]ScenarioFile, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}

	var scenarios []ScenarioFile
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}

		var sf ScenarioFile
		if err := yaml.Unmarshal(data, &sf); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", f, err)
		}
		scenarios = append(scenarios, sf)
	}

	return scenarios, nil
}

func shouldSkip(scenario Scenario, shell string) bool {
	// Skip interactive tests for now
	if scenario.Interactive {
		return true
	}

	// Check OS skip
	for _, os := range scenario.SkipOS {
		if os == runtime.GOOS {
			return true
		}
	}

	// Check shell skip
	for _, s := range scenario.SkipShells {
		if s == shell {
			return true
		}
	}

	return false
}

// envWithShell returns os.Environ() with any existing SHELL entry replaced
// by shell, so the override is deterministic regardless of whether the
// underlying OS/runtime resolves duplicate env entries by first or last
// occurrence.
func envWithShell(shell string) []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "SHELL=") {
			env = append(env, kv)
		}
	}
	return append(env, "SHELL="+shell)
}

func runScenario(wtBinary, shell, fileName string, scenario Scenario, verbose, showOutput, keepTmp bool) Result {
	result := Result{
		Scenario: fmt.Sprintf("%s/%s", fileName, scenario.Name),
		Shell:    shell,
	}

	// Generate test script
	script := generateScript(wtBinary, shell, scenario, verbose, showOutput, keepTmp)

	if verbose {
		fmt.Printf("--- Script for %s ---\n%s\n---\n", scenario.Name, script)
	}

	// Execute script
	var cmd *exec.Cmd
	if shell == "powershell" || shell == "pwsh" {
		cmd = exec.Command(shell, "-NoProfile", "-Command", script)
	} else {
		cmd = exec.Command(shell, "-c", script)
		// Override $SHELL to match the interpreter running this script.
		// wt shellenv auto-detects its target shell from $SHELL when no
		// explicit argument is given, so a mismatched ambient $SHELL (e.g.
		// fish on the host machine) would otherwise produce output that
		// doesn't match what bash/zsh expect.
		cmd.Env = envWithShell(shell)
	}

	output, err := cmd.CombinedOutput()
	result.Output = string(output)

	if err != nil {
		// Check if it's an expected failure
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.Error = fmt.Sprintf("exit code %d", exitErr.ExitCode())
		} else {
			result.Error = err.Error()
		}
		result.Passed = false
		return result
	}

	result.Passed = true
	return result
}

func generateScript(wtBinary, shell string, scenario Scenario, verbose, showOutput, keepTmp bool) string {
	if shell == "fish" {
		return generateFishScript(wtBinary, scenario, verbose, showOutput, keepTmp)
	}
	if shell == "powershell" || shell == "pwsh" {
		return generatePowerShellScript(wtBinary, scenario, verbose, showOutput, keepTmp)
	}
	return generatePosixScript(wtBinary, shell, scenario, verbose, showOutput, keepTmp)
}

func generatePosixScript(wtBinary, shell string, scenario Scenario, verbose, showOutput, keepTmp bool) string {
	var sb strings.Builder

	// Header
	sb.WriteString("set -e\n")
	fmt.Fprintf(&sb, "export WT_BIN='%s'\n", wtBinary)
	sb.WriteString("TEST_DIR=$(mktemp -d)\n")
	sb.WriteString("REPO_DIR=\"$TEST_DIR/test-repo\"\n")
	sb.WriteString("REPO_NAME=\"test-repo\"\n")
	sb.WriteString("export WORKTREE_ROOT=\"$TEST_DIR/worktrees\"\n")
	sb.WriteString("mkdir -p \"$REPO_DIR\"\n")
	sb.WriteString("cd \"$REPO_DIR\"\n")
	sb.WriteString("git init --quiet\n")
	sb.WriteString("git config user.email 'test@example.com'\n")
	sb.WriteString("git config user.name 'Test User'\n")
	sb.WriteString("echo 'initial' > README.md\n")
	sb.WriteString("git add README.md\n")
	sb.WriteString("git commit -m 'initial' --quiet\n")
	sb.WriteString("git branch -M main\n")
	fmt.Fprintf(&sb, "export PATH=\"%s:$PATH\"\n", filepath.Dir(wtBinary))

	// Setup steps
	for _, setup := range scenario.Setup {
		if setup.CreateBranch != "" {
			fmt.Fprintf(&sb, "git checkout -b '%s' --quiet\n", setup.CreateBranch)
			fmt.Fprintf(&sb, "git commit --allow-empty -m 'commit on %s' --quiet\n", setup.CreateBranch)
			sb.WriteString("git checkout main --quiet\n")
		}
		if setup.CreateFile != nil {
			fmt.Fprintf(&sb, "echo '%s' > '%s'\n", setup.CreateFile.Content, setup.CreateFile.Path)
		}
		if setup.GitAdd != "" {
			fmt.Fprintf(&sb, "git add '%s'\n", setup.GitAdd)
		}
		if setup.GitCommit != "" {
			fmt.Fprintf(&sb, "git commit -m '%s' --quiet\n", setup.GitCommit)
		}
		if setup.GitCheckout != "" {
			fmt.Fprintf(&sb, "git checkout '%s' --quiet\n", setup.GitCheckout)
		}
	}

	// Source shellenv unless skipped. Pass the shell explicitly so output
	// matches the interpreter running this script, regardless of the
	// ambient $SHELL env var (which may differ, e.g. fish on the host).
	if !scenario.SkipShellenv {
		fmt.Fprintf(&sb, "eval \"$($WT_BIN shellenv %s)\"\n", shell)
	}

	// Test steps
	for _, step := range scenario.Steps {
		if step.Cd != "" {
			cd := step.Cd
			cd = strings.ReplaceAll(cd, "$REPO_DIR", "\"$REPO_DIR\"")
			fmt.Fprintf(&sb, "cd %s\n", cd)
		}
		if step.Run != "" {
			// Set step-level environment variables
			if len(step.Env) > 0 {
				keys := make([]string, 0, len(step.Env))
				for k := range step.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&sb, "export %s='%s'\n", k, step.Env[k])
				}
			}

			runCmd := step.Run
			needsOutput := step.Expect != nil && (step.Expect.OutputContains != "" || step.Expect.OutputNotContains != "")
			expectsNonZero := step.Expect != nil && step.Expect.ExitCode != nil && *step.Expect.ExitCode != 0

			if expectsNonZero {
				// Disable set -e for commands that expect non-zero exit
				sb.WriteString("set +e\n")
				if needsOutput {
					fmt.Fprintf(&sb, "__output=$(%s 2>&1)\n", runCmd)
				} else {
					fmt.Fprintf(&sb, "%s\n", runCmd)
				}
				sb.WriteString("__exit_code=$?\n")
				sb.WriteString("set -e\n")
			} else {
				// Normal execution with set -e active
				if needsOutput {
					fmt.Fprintf(&sb, "__output=$(%s 2>&1) || __exit_code=$?\n", runCmd)
					sb.WriteString("__exit_code=${__exit_code:-0}\n")
				} else {
					fmt.Fprintf(&sb, "%s || __exit_code=$?\n", runCmd)
					sb.WriteString("__exit_code=${__exit_code:-0}\n")
				}
			}

			if step.Expect != nil {
				if step.Expect.ExitCode != nil {
					fmt.Fprintf(&sb, "[ \"$__exit_code\" -eq %d ] || { echo \"Expected exit code %d, got $__exit_code\"; exit 1; }\n",
						*step.Expect.ExitCode, *step.Expect.ExitCode)
				}
				if step.Expect.CwdEndsWith != "" {
					fmt.Fprintf(&sb, "case \"$(pwd)\" in *%s) ;; *) echo \"CWD $(pwd) doesn't end with %s\"; exit 1;; esac\n",
						step.Expect.CwdEndsWith, step.Expect.CwdEndsWith)
				}
				if step.Expect.Branch != "" {
					fmt.Fprintf(&sb, "[ \"$(git branch --show-current)\" = '%s' ] || { echo \"Expected branch %s\"; exit 1; }\n",
						step.Expect.Branch, step.Expect.Branch)
				}
				if step.Expect.OutputContains != "" {
					contains := strings.ReplaceAll(step.Expect.OutputContains, "'", "'\\''")
					fmt.Fprintf(&sb, "echo \"$__output\" | grep -F -q -- '%s' || { echo \"Output missing expected substring\"; exit 1; }\n",
						contains)
				}
				if step.Expect.OutputNotContains != "" {
					notContains := strings.ReplaceAll(step.Expect.OutputNotContains, "'", "'\\''")
					fmt.Fprintf(&sb, "echo \"$__output\" | grep -F -q -- '%s' && { echo \"Output should not contain expected substring\"; exit 1; } || true\n",
						notContains)
				}
			}
		}
	}

	if showOutput {
		sb.WriteString("echo \"TEST_DIR=$TEST_DIR\"\n")
		sb.WriteString("find \"$TEST_DIR\" -path '*/.git' -print -prune -o -print\n")
	}

	if verbose {
		sb.WriteString("echo \"TEST_DIR=$TEST_DIR\"\n")
		sb.WriteString("ls -R \"$TEST_DIR\"\n")
	}

	if keepTmp {
		sb.WriteString("echo \"__TEST_DIR__=$TEST_DIR\"\n")
	}

	// Cleanup
	if !keepTmp {
		sb.WriteString("rm -rf \"$TEST_DIR\"\n")
	}

	return sb.String()
}

// fishSingleQuote escapes a string for embedding inside a fish single-quoted
// literal. Unlike POSIX shells, fish allows backslash-escaping directly
// inside single quotes: only \\ and \' are special there.
func fishSingleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// generateFishScript generates a test script using fish syntax. Fish is not
// POSIX-compatible (e.g. `VAR=value` and `export VAR=value` are syntax
// errors in fish), so it needs its own generator rather than reusing
// generatePosixScript.
func generateFishScript(wtBinary string, scenario Scenario, verbose, showOutput, keepTmp bool) string {
	var sb strings.Builder

	// Header. Fish has no errexit ("set -e" in the bash sense), so critical
	// setup commands explicitly abort via "; or exit 1".
	fmt.Fprintf(&sb, "set -x WT_BIN '%s'\n", fishSingleQuote(wtBinary))
	sb.WriteString("set TEST_DIR (mktemp -d)\n")
	sb.WriteString("set REPO_DIR \"$TEST_DIR/test-repo\"\n")
	sb.WriteString("set REPO_NAME \"test-repo\"\n")
	sb.WriteString("set -x WORKTREE_ROOT \"$TEST_DIR/worktrees\"\n")
	sb.WriteString("mkdir -p \"$REPO_DIR\"; or exit 1\n")
	sb.WriteString("cd \"$REPO_DIR\"; or exit 1\n")
	sb.WriteString("git init --quiet; or exit 1\n")
	sb.WriteString("git config user.email 'test@example.com'\n")
	sb.WriteString("git config user.name 'Test User'\n")
	sb.WriteString("echo 'initial' > README.md\n")
	sb.WriteString("git add README.md; or exit 1\n")
	sb.WriteString("git commit -m 'initial' --quiet; or exit 1\n")
	sb.WriteString("git branch -M main; or exit 1\n")
	fmt.Fprintf(&sb, "set -x PATH '%s' $PATH\n", fishSingleQuote(filepath.Dir(wtBinary)))

	// Setup steps
	for _, setup := range scenario.Setup {
		if setup.CreateBranch != "" {
			fmt.Fprintf(&sb, "git checkout -b '%s' --quiet; or exit 1\n", setup.CreateBranch)
			fmt.Fprintf(&sb, "git commit --allow-empty -m 'commit on %s' --quiet; or exit 1\n", setup.CreateBranch)
			sb.WriteString("git checkout main --quiet; or exit 1\n")
		}
		if setup.CreateFile != nil {
			fmt.Fprintf(&sb, "echo '%s' > '%s'\n", setup.CreateFile.Content, setup.CreateFile.Path)
		}
		if setup.GitAdd != "" {
			fmt.Fprintf(&sb, "git add '%s'; or exit 1\n", setup.GitAdd)
		}
		if setup.GitCommit != "" {
			fmt.Fprintf(&sb, "git commit -m '%s' --quiet; or exit 1\n", setup.GitCommit)
		}
		if setup.GitCheckout != "" {
			fmt.Fprintf(&sb, "git checkout '%s' --quiet; or exit 1\n", setup.GitCheckout)
		}
	}

	// Source shellenv unless skipped
	if !scenario.SkipShellenv {
		sb.WriteString("$WT_BIN shellenv fish | source; or exit 1\n")
	}

	// Test steps
	for _, step := range scenario.Steps {
		if step.Cd != "" {
			cd := step.Cd
			cd = strings.ReplaceAll(cd, "$REPO_DIR", "\"$REPO_DIR\"")
			fmt.Fprintf(&sb, "cd %s; or exit 1\n", cd)
		}
		if step.Run != "" {
			// Set step-level environment variables
			if len(step.Env) > 0 {
				keys := make([]string, 0, len(step.Env))
				for k := range step.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&sb, "set -x %s '%s'\n", k, fishSingleQuote(step.Env[k]))
				}
			}

			// Trim trailing newlines so "2>&1" attaches to the last command
			// line instead of becoming its own line — fish (unlike bash)
			// rejects a bare redirection with no preceding command.
			runCmd := strings.TrimRight(step.Run, "\n")
			needsOutput := step.Expect != nil && (step.Expect.OutputContains != "" || step.Expect.OutputNotContains != "")

			// Fish has no errexit to disable/re-enable around commands that
			// expect a non-zero exit, so both cases capture $status the same way.
			if needsOutput {
				fmt.Fprintf(&sb, "set __output (%s 2>&1)\n", runCmd)
				sb.WriteString("set __exit_code $status\n")
			} else {
				fmt.Fprintf(&sb, "%s\n", runCmd)
				sb.WriteString("set __exit_code $status\n")
			}

			if step.Expect != nil {
				if step.Expect.ExitCode != nil {
					fmt.Fprintf(&sb, "if test $__exit_code -ne %d; echo \"Expected exit code %d, got $__exit_code\"; exit 1; end\n",
						*step.Expect.ExitCode, *step.Expect.ExitCode)
				}
				if step.Expect.CwdEndsWith != "" {
					fmt.Fprintf(&sb, "set __cwd (pwd); switch $__cwd; case '*%s'; case '*'; echo \"CWD $__cwd doesn't end with %s\"; exit 1; end\n",
						step.Expect.CwdEndsWith, step.Expect.CwdEndsWith)
				}
				if step.Expect.Branch != "" {
					fmt.Fprintf(&sb, "set __branch (git branch --show-current); if test \"$__branch\" != '%s'; echo \"Expected branch %s\"; exit 1; end\n",
						step.Expect.Branch, step.Expect.Branch)
				}
				if step.Expect.OutputContains != "" {
					contains := fishSingleQuote(step.Expect.OutputContains)
					fmt.Fprintf(&sb, "echo \"$__output\" | grep -F -q -- '%s'; or begin; echo \"Output missing expected substring\"; exit 1; end\n",
						contains)
				}
				if step.Expect.OutputNotContains != "" {
					notContains := fishSingleQuote(step.Expect.OutputNotContains)
					fmt.Fprintf(&sb, "echo \"$__output\" | grep -F -q -- '%s'; and begin; echo \"Output should not contain expected substring\"; exit 1; end\n",
						notContains)
				}
			}
		}
	}

	if showOutput {
		sb.WriteString("echo \"TEST_DIR=$TEST_DIR\"\n")
		sb.WriteString("find \"$TEST_DIR\" -path '*/.git' -print -prune -o -print\n")
	}

	if verbose {
		sb.WriteString("echo \"TEST_DIR=$TEST_DIR\"\n")
		sb.WriteString("ls -R \"$TEST_DIR\"\n")
	}

	if keepTmp {
		sb.WriteString("echo \"__TEST_DIR__=$TEST_DIR\"\n")
	}

	// Cleanup
	if !keepTmp {
		sb.WriteString("rm -rf \"$TEST_DIR\"\n")
	}

	return sb.String()
}

func generatePowerShellScript(wtBinary string, scenario Scenario, verbose, showOutput, keepTmp bool) string {
	var sb strings.Builder

	// Header
	sb.WriteString("$ErrorActionPreference = 'Stop'\n")
	fmt.Fprintf(&sb, "$env:WT_BIN = '%s'\n", wtBinary)
	sb.WriteString("$__tmpBase = [System.IO.Path]::GetTempPath()\n")
	sb.WriteString("if (-not $__tmpBase) { $__tmpBase = $env:TEMP }\n")
	sb.WriteString("if (-not $__tmpBase) { $__tmpBase = $env:TMP }\n")
	sb.WriteString("if (-not $__tmpBase) { $__tmpBase = $PWD.Path }\n")
	sb.WriteString("$TestDir = Join-Path $__tmpBase \"wt-e2e-$(Get-Random)\"\n")
	sb.WriteString("$RepoDir = Join-Path $TestDir 'test-repo'\n")
	sb.WriteString("$env:WORKTREE_ROOT = Join-Path $TestDir 'worktrees'\n")
	sb.WriteString("New-Item -ItemType Directory -Path $RepoDir -Force | Out-Null\n")
	sb.WriteString("Push-Location $RepoDir\n")
	sb.WriteString("git init --quiet\n")
	sb.WriteString("git config user.email 'test@example.com'\n")
	sb.WriteString("git config user.name 'Test User'\n")
	sb.WriteString("Set-Content -Path 'README.md' -Value 'initial'\n")
	sb.WriteString("git add 'README.md'\n")
	sb.WriteString("git commit -m 'initial' --quiet\n")
	sb.WriteString("git branch -M main\n")
	fmt.Fprintf(&sb, "$env:PATH = '%s;' + $env:PATH\n", filepath.Dir(wtBinary))

	// Setup steps
	for _, setup := range scenario.Setup {
		if setup.CreateBranch != "" {
			fmt.Fprintf(&sb, "git checkout -b '%s' --quiet\n", setup.CreateBranch)
			fmt.Fprintf(&sb, "git commit --allow-empty -m 'commit on %s' --quiet\n", setup.CreateBranch)
			sb.WriteString("git checkout main --quiet\n")
		}
		if setup.CreateFile != nil {
			fmt.Fprintf(&sb, "Set-Content -Path '%s' -Value '%s'\n", setup.CreateFile.Path, setup.CreateFile.Content)
		}
		if setup.GitAdd != "" {
			fmt.Fprintf(&sb, "git add '%s'\n", setup.GitAdd)
		}
		if setup.GitCommit != "" {
			fmt.Fprintf(&sb, "git commit -m '%s' --quiet\n", setup.GitCommit)
		}
		if setup.GitCheckout != "" {
			fmt.Fprintf(&sb, "git checkout '%s' --quiet\n", setup.GitCheckout)
		}
	}

	// Source shellenv unless skipped
	if !scenario.SkipShellenv {
		sb.WriteString("$shellenv = & $env:WT_BIN shellenv\n")
		sb.WriteString("Invoke-Expression ($shellenv -join \"`n\")\n")
	}

	// Test steps
	for _, step := range scenario.Steps {
		if step.Cd != "" {
			cd := step.Cd
			cd = strings.ReplaceAll(cd, "$REPO_DIR", "$RepoDir")
			fmt.Fprintf(&sb, "Set-Location %s\n", cd)
		}
		if step.Run != "" {
			// Set step-level environment variables
			if len(step.Env) > 0 {
				keys := make([]string, 0, len(step.Env))
				for k := range step.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&sb, "$env:%s = '%s'\n", k, step.Env[k])
				}
			}

			runCmd := step.Run
			// Translate bash variables to PowerShell syntax
			runCmd = strings.ReplaceAll(runCmd, "$WT_BIN", "$env:WT_BIN")
			runCmd = strings.ReplaceAll(runCmd, "$WORKTREE_ROOT", "$env:WORKTREE_ROOT")
			runCmd = strings.ReplaceAll(runCmd, "$REPO_NAME", "'test-repo'")

			// Add & call operator when command starts with a variable (like $env:WT_BIN)
			if strings.HasPrefix(runCmd, "$env:") {
				runCmd = "& " + runCmd
			}

			needsOutput := step.Expect != nil && (step.Expect.OutputContains != "" || step.Expect.OutputNotContains != "")
			expectsNonZero := step.Expect != nil && step.Expect.ExitCode != nil && *step.Expect.ExitCode != 0

			if needsOutput {
				sb.WriteString("$__output = ''\n")
			}

			if expectsNonZero {
				// Handle expected non-zero exit codes
				sb.WriteString("$__exit_code = 0\n")
				sb.WriteString("try {\n")
				if needsOutput {
					fmt.Fprintf(&sb, "  $__output = %s 2>&1 | Out-String\n", runCmd)
				} else {
					fmt.Fprintf(&sb, "  %s\n", runCmd)
				}
				sb.WriteString("  $__exit_code = $LASTEXITCODE\n")
				sb.WriteString("} catch {\n")
				if needsOutput {
					sb.WriteString("  $__output = ($_ | Out-String)\n")
				}
				sb.WriteString("  $__exit_code = 1\n")
				sb.WriteString("}\n")
			} else if needsOutput {
				// Capture output (runs in pipeline context)
				fmt.Fprintf(&sb, "$__output = %s 2>&1 | Out-String\n", runCmd)
				sb.WriteString("$__exit_code = $LASTEXITCODE\n")
			} else {
				// Run directly to allow auto-cd to work
				fmt.Fprintf(&sb, "%s\n", runCmd)
				sb.WriteString("$__exit_code = $LASTEXITCODE\n")
			}

			if step.Expect != nil {
				if step.Expect.ExitCode != nil {
					fmt.Fprintf(&sb, "if ($__exit_code -ne %d) { throw \"Expected exit code %d, got $__exit_code\" }\n",
						*step.Expect.ExitCode, *step.Expect.ExitCode)
				}
				if step.Expect.CwdEndsWith != "" {
					// Handle both forward and back slashes for cross-platform compatibility
					suffix := step.Expect.CwdEndsWith
					sb.WriteString("$__cwd = (Get-Location).Path.Replace('\\', '/')\n")
					fmt.Fprintf(&sb, "if (-not $__cwd.EndsWith('%s')) { throw \"CWD $__cwd doesn't end with %s\" }\n",
						suffix, suffix)
				}
				if step.Expect.Branch != "" {
					sb.WriteString("$__branch = git branch --show-current\n")
					fmt.Fprintf(&sb, "if ($__branch -ne '%s') { throw \"Expected branch %s, got $__branch\" }\n",
						step.Expect.Branch, step.Expect.Branch)
				}
				if step.Expect.OutputContains != "" {
					contains := strings.ReplaceAll(step.Expect.OutputContains, "'", "''")
					sb.WriteString("if ($null -eq $__output) { $__output = '' }\n")
					fmt.Fprintf(&sb, "if (-not $__output.Contains('%s')) { throw \"Output missing expected substring\" }\n",
						contains)
				}
				if step.Expect.OutputNotContains != "" {
					notContains := strings.ReplaceAll(step.Expect.OutputNotContains, "'", "''")
					sb.WriteString("if ($null -eq $__output) { $__output = '' }\n")
					fmt.Fprintf(&sb, "if ($__output.Contains('%s')) { throw \"Output should not contain expected substring\" }\n",
						notContains)
				}
			}
		}
	}

	// Cleanup
	sb.WriteString("Pop-Location\n")
	if showOutput {
		sb.WriteString("Write-Host \"TEST_DIR=$TestDir\"\n")
		sb.WriteString("Get-ChildItem -Path $TestDir -Force -Recurse | ForEach-Object { $_.FullName }\n")
	}
	if verbose {
		sb.WriteString("Write-Host \"TEST_DIR=$TestDir\"\n")
		sb.WriteString("Get-ChildItem -Path $TestDir -Recurse | ForEach-Object { $_.FullName }\n")
	}
	if !keepTmp {
		sb.WriteString("Remove-Item -Recurse -Force $TestDir -ErrorAction SilentlyContinue\n")
	}

	return sb.String()
}

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
	Run    string  `yaml:"run"`
	Cd     string  `yaml:"cd"`
	Expect *Expect `yaml:"expect"`
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
				result := runScenario(binary, shell, file.Name, scenario, *verbose)

				if result.Passed {
					fmt.Printf("PASS: %s/%s\n", file.Name, scenario.Name)
					passed++
				} else {
					fmt.Printf("FAIL: %s/%s\n", file.Name, scenario.Name)
					if result.Error != "" {
						fmt.Printf("  Error: %s\n", result.Error)
					}
					if *verbose && result.Output != "" {
						fmt.Printf("  Output: %s\n", result.Output)
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

func runScenario(wtBinary, shell, fileName string, scenario Scenario, verbose bool) Result {
	result := Result{
		Scenario: fmt.Sprintf("%s/%s", fileName, scenario.Name),
		Shell:    shell,
	}

	// Generate test script
	script := generateScript(wtBinary, shell, scenario)

	if verbose {
		fmt.Printf("--- Script for %s ---\n%s\n---\n", scenario.Name, script)
	}

	// Execute script
	var cmd *exec.Cmd
	if shell == "powershell" || shell == "pwsh" {
		cmd = exec.Command(shell, "-NoProfile", "-Command", script)
	} else {
		cmd = exec.Command(shell, "-c", script)
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

func generateScript(wtBinary, shell string, scenario Scenario) string {
	if shell == "powershell" || shell == "pwsh" {
		return generatePowerShellScript(wtBinary, scenario)
	}
	return generatePosixScript(wtBinary, shell, scenario)
}

func generatePosixScript(wtBinary, shell string, scenario Scenario) string {
	var sb strings.Builder

	// Header
	sb.WriteString("set -e\n")
	sb.WriteString(fmt.Sprintf("export WT_BIN='%s'\n", wtBinary))
	sb.WriteString("TEST_DIR=$(mktemp -d)\n")
	sb.WriteString("REPO_DIR=\"$TEST_DIR/test-repo\"\n")
	sb.WriteString("REPO_NAME=\"test-repo\"\n")
	sb.WriteString("export WORKTREE_ROOT=\"$TEST_DIR/worktrees\"\n")
	sb.WriteString("mkdir -p \"$REPO_DIR\" \"$WORKTREE_ROOT\"\n")
	sb.WriteString("cd \"$REPO_DIR\"\n")
	sb.WriteString("git init --quiet\n")
	sb.WriteString("git config user.email 'test@example.com'\n")
	sb.WriteString("git config user.name 'Test User'\n")
	sb.WriteString("git commit --allow-empty -m 'initial' --quiet\n")
	sb.WriteString("git branch -M main\n")
	sb.WriteString(fmt.Sprintf("export PATH=\"%s:$PATH\"\n", filepath.Dir(wtBinary)))

	// Setup steps
	for _, setup := range scenario.Setup {
		if setup.CreateBranch != "" {
			sb.WriteString(fmt.Sprintf("git checkout -b '%s' --quiet\n", setup.CreateBranch))
			sb.WriteString(fmt.Sprintf("git commit --allow-empty -m 'commit on %s' --quiet\n", setup.CreateBranch))
			sb.WriteString("git checkout main --quiet\n")
		}
		if setup.CreateFile != nil {
			sb.WriteString(fmt.Sprintf("echo '%s' > '%s'\n", setup.CreateFile.Content, setup.CreateFile.Path))
		}
		if setup.GitAdd != "" {
			sb.WriteString(fmt.Sprintf("git add '%s'\n", setup.GitAdd))
		}
		if setup.GitCommit != "" {
			sb.WriteString(fmt.Sprintf("git commit -m '%s' --quiet\n", setup.GitCommit))
		}
		if setup.GitCheckout != "" {
			sb.WriteString(fmt.Sprintf("git checkout '%s' --quiet\n", setup.GitCheckout))
		}
	}

	// Source shellenv unless skipped
	if !scenario.SkipShellenv {
		sb.WriteString("eval \"$($WT_BIN shellenv)\"\n")
	}

	// Test steps
	for _, step := range scenario.Steps {
		if step.Cd != "" {
			cd := step.Cd
			cd = strings.ReplaceAll(cd, "$REPO_DIR", "\"$REPO_DIR\"")
			sb.WriteString(fmt.Sprintf("cd %s\n", cd))
		}
		if step.Run != "" {
			runCmd := step.Run
			needsOutput := step.Expect != nil && (step.Expect.OutputContains != "" || step.Expect.OutputNotContains != "")
			expectsNonZero := step.Expect != nil && step.Expect.ExitCode != nil && *step.Expect.ExitCode != 0

			if expectsNonZero {
				// Disable set -e for commands that expect non-zero exit
				sb.WriteString("set +e\n")
				if needsOutput {
					sb.WriteString(fmt.Sprintf("__output=$(%s 2>&1)\n", runCmd))
				} else {
					sb.WriteString(fmt.Sprintf("%s\n", runCmd))
				}
				sb.WriteString("__exit_code=$?\n")
				sb.WriteString("set -e\n")
			} else {
				// Normal execution with set -e active
				if needsOutput {
					sb.WriteString(fmt.Sprintf("__output=$(%s 2>&1) || __exit_code=$?\n", runCmd))
					sb.WriteString("__exit_code=${__exit_code:-0}\n")
				} else {
					sb.WriteString(fmt.Sprintf("%s || __exit_code=$?\n", runCmd))
					sb.WriteString("__exit_code=${__exit_code:-0}\n")
				}
			}

			if step.Expect != nil {
				if step.Expect.ExitCode != nil {
					sb.WriteString(fmt.Sprintf("[ \"$__exit_code\" -eq %d ] || { echo \"Expected exit code %d, got $__exit_code\"; exit 1; }\n",
						*step.Expect.ExitCode, *step.Expect.ExitCode))
				}
				if step.Expect.CwdEndsWith != "" {
					sb.WriteString(fmt.Sprintf("case \"$(pwd)\" in *%s) ;; *) echo \"CWD $(pwd) doesn't end with %s\"; exit 1;; esac\n",
						step.Expect.CwdEndsWith, step.Expect.CwdEndsWith))
				}
				if step.Expect.Branch != "" {
					sb.WriteString(fmt.Sprintf("[ \"$(git branch --show-current)\" = '%s' ] || { echo \"Expected branch %s\"; exit 1; }\n",
						step.Expect.Branch, step.Expect.Branch))
				}
				if step.Expect.OutputContains != "" {
					sb.WriteString(fmt.Sprintf("echo \"$__output\" | grep -q '%s' || { echo \"Output missing '%s'\"; exit 1; }\n",
						step.Expect.OutputContains, step.Expect.OutputContains))
				}
				if step.Expect.OutputNotContains != "" {
					sb.WriteString(fmt.Sprintf("echo \"$__output\" | grep -q '%s' && { echo \"Output should not contain '%s'\"; exit 1; } || true\n",
						step.Expect.OutputNotContains, step.Expect.OutputNotContains))
				}
			}
		}
	}

	// Cleanup
	sb.WriteString("rm -rf \"$TEST_DIR\"\n")

	return sb.String()
}

func generatePowerShellScript(wtBinary string, scenario Scenario) string {
	var sb strings.Builder

	// Header
	sb.WriteString("$ErrorActionPreference = 'Stop'\n")
	sb.WriteString(fmt.Sprintf("$env:WT_BIN = '%s'\n", wtBinary))
	sb.WriteString("$TestDir = Join-Path $env:TEMP \"wt-e2e-$(Get-Random)\"\n")
	sb.WriteString("$RepoDir = Join-Path $TestDir 'test-repo'\n")
	sb.WriteString("$env:WORKTREE_ROOT = Join-Path $TestDir 'worktrees'\n")
	sb.WriteString("New-Item -ItemType Directory -Path $RepoDir -Force | Out-Null\n")
	sb.WriteString("New-Item -ItemType Directory -Path $env:WORKTREE_ROOT -Force | Out-Null\n")
	sb.WriteString("Push-Location $RepoDir\n")
	sb.WriteString("git init --quiet\n")
	sb.WriteString("git config user.email 'test@example.com'\n")
	sb.WriteString("git config user.name 'Test User'\n")
	sb.WriteString("git commit --allow-empty -m 'initial' --quiet\n")
	sb.WriteString("git branch -M main\n")
	sb.WriteString(fmt.Sprintf("$env:PATH = '%s;' + $env:PATH\n", filepath.Dir(wtBinary)))

	// Setup steps
	for _, setup := range scenario.Setup {
		if setup.CreateBranch != "" {
			sb.WriteString(fmt.Sprintf("git checkout -b '%s' --quiet\n", setup.CreateBranch))
			sb.WriteString(fmt.Sprintf("git commit --allow-empty -m 'commit on %s' --quiet\n", setup.CreateBranch))
			sb.WriteString("git checkout main --quiet\n")
		}
		if setup.CreateFile != nil {
			sb.WriteString(fmt.Sprintf("Set-Content -Path '%s' -Value '%s'\n", setup.CreateFile.Path, setup.CreateFile.Content))
		}
		if setup.GitAdd != "" {
			sb.WriteString(fmt.Sprintf("git add '%s'\n", setup.GitAdd))
		}
		if setup.GitCommit != "" {
			sb.WriteString(fmt.Sprintf("git commit -m '%s' --quiet\n", setup.GitCommit))
		}
		if setup.GitCheckout != "" {
			sb.WriteString(fmt.Sprintf("git checkout '%s' --quiet\n", setup.GitCheckout))
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
			sb.WriteString(fmt.Sprintf("Set-Location %s\n", cd))
		}
		if step.Run != "" {
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

			if expectsNonZero {
				// Handle expected non-zero exit codes
				sb.WriteString("$__exit_code = 0\n")
				sb.WriteString("try {\n")
				if needsOutput {
					sb.WriteString(fmt.Sprintf("  $__output = %s 2>&1 | Out-String\n", runCmd))
				} else {
					sb.WriteString(fmt.Sprintf("  %s\n", runCmd))
				}
				sb.WriteString("  $__exit_code = $LASTEXITCODE\n")
				sb.WriteString("} catch {\n")
				sb.WriteString("  $__exit_code = 1\n")
				sb.WriteString("}\n")
			} else if needsOutput {
				// Capture output (runs in pipeline context)
				sb.WriteString(fmt.Sprintf("$__output = %s 2>&1 | Out-String\n", runCmd))
				sb.WriteString("$__exit_code = $LASTEXITCODE\n")
			} else {
				// Run directly to allow auto-cd to work
				sb.WriteString(fmt.Sprintf("%s\n", runCmd))
				sb.WriteString("$__exit_code = $LASTEXITCODE\n")
			}

			if step.Expect != nil {
				if step.Expect.ExitCode != nil {
					sb.WriteString(fmt.Sprintf("if ($__exit_code -ne %d) { throw \"Expected exit code %d, got $__exit_code\" }\n",
						*step.Expect.ExitCode, *step.Expect.ExitCode))
				}
				if step.Expect.CwdEndsWith != "" {
					// Handle both forward and back slashes for cross-platform compatibility
					suffix := step.Expect.CwdEndsWith
					sb.WriteString("$__cwd = (Get-Location).Path.Replace('\\', '/')\n")
					sb.WriteString(fmt.Sprintf("if (-not $__cwd.EndsWith('%s')) { throw \"CWD $__cwd doesn't end with %s\" }\n",
						suffix, suffix))
				}
				if step.Expect.Branch != "" {
					sb.WriteString("$__branch = git branch --show-current\n")
					sb.WriteString(fmt.Sprintf("if ($__branch -ne '%s') { throw \"Expected branch %s, got $__branch\" }\n",
						step.Expect.Branch, step.Expect.Branch))
				}
				if step.Expect.OutputContains != "" {
					sb.WriteString(fmt.Sprintf("if (-not $__output.Contains('%s')) { throw \"Output missing '%s'\" }\n",
						step.Expect.OutputContains, step.Expect.OutputContains))
				}
				if step.Expect.OutputNotContains != "" {
					sb.WriteString(fmt.Sprintf("if ($__output.Contains('%s')) { throw \"Output should not contain '%s'\" }\n",
						step.Expect.OutputNotContains, step.Expect.OutputNotContains))
				}
			}
		}
	}

	// Cleanup
	sb.WriteString("Pop-Location\n")
	sb.WriteString("Remove-Item -Recurse -Force $TestDir -ErrorAction SilentlyContinue\n")

	return sb.String()
}

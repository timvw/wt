package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
)

// getHooks returns the hook commands for a given hook name.
func getHooks(hookName string) []string {
	switch hookName {
	case "pre_create":
		return worktreeHooks.PreCreate
	case "post_create":
		return worktreeHooks.PostCreate
	case "pre_checkout":
		return worktreeHooks.PreCheckout
	case "post_checkout":
		return worktreeHooks.PostCheckout
	case "pre_remove":
		return worktreeHooks.PreRemove
	case "post_remove":
		return worktreeHooks.PostRemove
	case "pre_pr":
		return worktreeHooks.PrePR
	case "post_pr":
		return worktreeHooks.PostPR
	case "pre_mr":
		return worktreeHooks.PreMR
	case "post_mr":
		return worktreeHooks.PostMR
	case "pre_clone":
		return worktreeHooks.PreClone
	case "post_clone":
		return worktreeHooks.PostClone
	default:
		return nil
	}
}

// buildHookEnv creates the environment variables map for hook commands. Path
// values are in the platform's native form; runHooks adapts them to the shell
// it ends up spawning.
func buildHookEnv(info repoInfo, branch, worktreePath string) map[string]string {
	return map[string]string{
		"WT_PATH":       worktreePath,
		"WT_BRANCH":     branch,
		"WT_MAIN":       info.Main,
		"WT_REPO_NAME":  info.Name,
		"WT_REPO_HOST":  info.Host,
		"WT_REPO_OWNER": info.Owner,
	}
}

// hookPathVars are the hook variables whose values are filesystem paths, and so
// are the ones that need adapting when the hook shell disagrees with the
// platform about what a path looks like.
var hookPathVars = []string{"WT_PATH", "WT_MAIN"}

// hookShell returns the interpreter and its command flag for running hook
// bodies, and whether that interpreter is POSIX.
//
// wt spawns hooks itself, so the shell the user is sitting in has no say in
// what runs them — which is why Windows needs a choice made here rather than
// inherited. A Git Bash, MSYS2 or Cygwin user writes POSIX hooks (every example
// in our own docs is one) and gets them run by `cmd`, which expands neither
// $WT_PATH nor knows `test`, `cp` or `&&`.
//
// The rule is deliberately conservative: use sh only where we can demonstrably
// see a POSIX environment (isPOSIXShellEnv) *and* find an sh to run it with.
// Anyone in PowerShell or cmd keeps cmd /c, so cmd-flavoured hooks written
// against the old behaviour keep working.
//
// lookPath is taken as an argument rather than called directly so the
// "POSIX env but no sh on PATH" branch is reachable from a test on any host.
func hookShell(goos string, lookPath func(string) (string, error)) (name, flag string, posix bool) {
	if goos != "windows" {
		return "sh", "-c", true
	}
	if isPOSIXShellEnv() {
		if sh, err := lookPath("sh"); err == nil {
			return sh, "-c", true
		}
	}
	return "cmd", "/c", false
}

// adaptHookEnv returns env with its path-valued variables rewritten for the
// shell that will run the hook. Only the Windows-native-paths-into-a-POSIX-shell
// combination needs anything doing; every other pairing already agrees on what
// a path looks like.
func adaptHookEnv(env map[string]string, goos string, shellIsPOSIX bool) map[string]string {
	if goos != "windows" || !shellIsPOSIX {
		return env
	}
	adapted := make(map[string]string, len(env))
	for k, v := range env {
		if slices.Contains(hookPathVars, k) {
			v = toPOSIXPath(v)
		}
		adapted[k] = v
	}
	return adapted
}

// toPOSIXPath rewrites a native Windows path into the mixed form that MSYS2,
// Git Bash and Cygwin all accept: C:\a\b -> C:/a/b. Backslashes are what
// actually break a POSIX hook — `cd $WT_PATH` eats them as escapes — so
// swapping the separator is the whole fix.
//
// Mixed form is preferred over the fully translated /c/a/b: it is understood by
// Cygwin too (which mounts drives under /cygdrive, not /c), and it survives
// being handed straight to a native tool the hook invokes, such as
// `code $WT_PATH` or `cd $WT_PATH && npm install`.
func toPOSIXPath(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// runHooks executes hook commands. For pre-hooks (hookName starts with "pre_"),
// any command failure aborts the operation. For post-hooks, failures are warned
// but do not fail the overall operation.
func runHooks(hookName string, hookCommands []string, env map[string]string) error {
	if os.Getenv("WT_HOOKS_DISABLED") == "1" {
		return nil
	}
	if len(hookCommands) == 0 {
		return nil
	}

	isPre := strings.HasPrefix(hookName, "pre_")
	shell, shellFlag, shellIsPOSIX := hookShell(runtime.GOOS, exec.LookPath)

	// Build environment slice from current env + hook vars
	environ := os.Environ()
	for k, v := range adaptHookEnv(env, runtime.GOOS, shellIsPOSIX) {
		environ = append(environ, fmt.Sprintf("%s=%s", k, v))
	}

	for _, cmdStr := range hookCommands {
		cmd := exec.Command(shell, shellFlag, cmdStr)
		cmd.Env = environ
		if isJSONOutput() {
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
		} else {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			if isPre {
				return fmt.Errorf("command %q failed: %w", cmdStr, err)
			}
			fmt.Fprintf(os.Stderr, "\u26a0 %s hook failed: command %q: %v\n", hookName, cmdStr, err)
		}
	}
	return nil
}

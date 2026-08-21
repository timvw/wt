package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var (
	copyDryRun bool
	copyForce  bool
	copyFrom   string
)

var copyCmd = &cobra.Command{
	Use:   "copy [branch]",
	Short: "Copy configured untracked files into a worktree",
	Long: `Copy the files declared in [files] into a worktree.

The same materialisation runs automatically on wt create/checkout/pr/mr; this
command re-runs it on demand, for instance after adding a pattern or after the
source file changed.

Nothing is copied unless [files] or a .worktreeinclude says so. Existing
destination files are skipped, not overwritten, unless --force is given.

Examples:
  wt copy                      # into the current worktree, from the main one
  wt copy feature-branch       # into feature-branch's worktree
  wt copy --dry-run            # show what would happen, change nothing
  wt copy --from other-branch  # seed from a sibling worktree instead of main`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		info, err := getRepoInfo()
		if err != nil {
			return err
		}

		dst, err := resolveCopyDestination(args)
		if err != nil {
			return err
		}
		src, err := resolveCopySource(info)
		if err != nil {
			return err
		}
		if filepath.Clean(src) == filepath.Clean(dst) {
			return fmt.Errorf("source and destination are the same worktree: %s", dst)
		}

		if filesDisabled() {
			return fmt.Errorf("file copying is disabled by WT_FILES_DISABLED=1")
		}

		result, err := runFileCopy(src, dst, copyOptions{DryRun: copyDryRun, Force: copyForce})
		if err != nil {
			return err
		}

		if isJSONOutput() {
			return emitJSONSuccess(cmd, result)
		}
		printCopyResult(result)
		return nil
	},
}

// resolveCopyDestination picks the worktree to copy into: the named branch's,
// or the one the user is standing in.
func resolveCopyDestination(args []string) (string, error) {
	if len(args) == 1 {
		path, ok := worktreeExists(args[0])
		if !ok {
			return "", fmt.Errorf("no worktree found for branch '%s'\nUse 'wt checkout %s' to create one", args[0], args[0])
		}
		return path, nil
	}

	// Without an argument the destination is implicit, which an automated
	// caller cannot see. Matching checkout/cd/remove/pr/mr, JSON mode requires
	// it to be spelled out.
	if isJSONOutput() {
		return "", fmt.Errorf("wt copy with --format json requires an explicit branch argument")
	}

	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a worktree; pass a branch name")
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveCopySource picks the worktree to copy from: --from, else the main
// worktree. --from accepts either a branch name or a path, so seeding from a
// sibling does not require looking its path up first.
func resolveCopySource(info repoInfo) (string, error) {
	if copyFrom == "" {
		if info.Main == "" {
			return "", fmt.Errorf("could not determine the main worktree; pass --from")
		}
		return info.Main, nil
	}

	if path, ok := worktreeExists(copyFrom); ok {
		return path, nil
	}
	expanded := expandHome(copyFrom)
	if stat, err := os.Stat(expanded); err == nil && stat.IsDir() {
		// Absolute, always: a link entry stores this path as the symlink's
		// target, and a relative --from would then be resolved against the
		// directory holding the link rather than the caller's cwd.
		abs, err := filepath.Abs(expanded)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	return "", fmt.Errorf("--from %q is neither a worktree branch nor an existing directory", copyFrom)
}

// printCopyResult renders the text output. A dry run lists every path, since
// seeing the list is the entire point; a real run prints only the tally.
func printCopyResult(result *copyResult) {
	if result == nil {
		return
	}

	if result.DryRun {
		if len(result.Files) == 0 {
			fmt.Println("Nothing to copy")
			return
		}
		width := 0
		for _, f := range result.Files {
			if len(f.Path) > width {
				width = len(f.Path)
			}
		}
		for _, f := range result.Files {
			fmt.Printf("would %-6s %-*s %s\n", copyVerb(f.Action), width, f.Path, copyDetail(f))
		}
		return
	}

	fmt.Println(formatCopySummary(result.Summary))
	for _, f := range result.Files {
		if f.Action == fileActionFailed {
			fmt.Fprintf(os.Stderr, "⚠ %s: %s\n", f.Path, f.Reason)
		}
	}
}

// copyVerb turns a past-tense action into the verb a dry run should use.
func copyVerb(action string) string {
	switch action {
	case fileActionCopied:
		return "copy"
	case fileActionLinked:
		return "link"
	case fileActionCreated:
		return "mkdir"
	case fileActionFailed:
		return "fail"
	default:
		return "skip"
	}
}

// copyDetail is the parenthetical explaining a dry-run line.
func copyDetail(f fileResult) string {
	switch f.Action {
	case fileActionLinked:
		return "-> " + f.Target
	case fileActionCopied:
		return "(" + f.Method + ")"
	case fileActionCreated:
		return "(directory)"
	default:
		return "(" + f.Reason + ")"
	}
}

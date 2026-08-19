package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"github.com/timvw/wt/internal/fuzzy"
)

var cdCmd = &cobra.Command{
	Use:     "cd [branch]",
	Aliases: []string{"sw"},
	Short:   "Switch to an existing worktree",
	Long: `Switch to an existing worktree.

Without arguments, shows a fuzzy-searchable list of the worktrees that
already exist. Unlike 'wt checkout', this never creates a worktree and
never lists branches that do not have one.`,
	Args: cobra.RangeArgs(0, 1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 {
			branch := args[0]
			path, exists := worktreeExists(branch)
			if !exists {
				return fmt.Errorf("no worktree found for branch: %s\nUse 'wt checkout %s' to create one", branch, branch)
			}
			return navigateToWorktree(cmd, branch, path)
		}

		if isJSONOutput() {
			return fmt.Errorf("wt cd with --format json requires an explicit branch argument")
		}

		targets, err := getSwitchableWorktrees()
		if err != nil {
			return fmt.Errorf("failed to get worktrees: %w", err)
		}
		if len(targets) == 0 {
			return fmt.Errorf("no worktrees to switch to")
		}

		labels := make([]string, len(targets))
		for i, target := range targets {
			labels[i] = target.Label
		}

		prompt := promptui.Select{
			Label:             "Select worktree",
			Items:             labels,
			Searcher:          fuzzy.Searcher(labels),
			StartInSearchMode: true,
			Stdout:            promptOutput(),
		}
		index, _, err := prompt.Run()
		if err != nil {
			return fmt.Errorf("selection cancelled")
		}

		selected := targets[index]
		return navigateToWorktree(cmd, selected.Branch, selected.Path)
	},
}

// switchTarget is a worktree that 'wt cd' can navigate to.
type switchTarget struct {
	Branch string
	Path   string
	Label  string
}

// getSwitchableWorktrees returns every existing worktree, including the main
// checkout. The current worktree is marked so it is obvious in the picker.
func getSwitchableWorktrees() ([]switchTarget, error) {
	entries, err := getWorktreeListPorcelain()
	if err != nil {
		return nil, err
	}

	cwd, _ := os.Getwd()
	return buildSwitchTargets(entries, cwd), nil
}

func buildSwitchTargets(entries []worktreeListEntry, cwd string) []switchTarget {
	targets := make([]switchTarget, 0, len(entries))
	for _, entry := range entries {
		if entry.Bare || entry.Path == "" {
			continue
		}

		label := entry.Branch
		if label == "" {
			// Detached worktrees have no branch: fall back to the directory name.
			label = fmt.Sprintf("%s (detached)", filepath.Base(entry.Path))
		}
		if isSameOrInsidePath(cwd, entry.Path) {
			label += " (current)"
		}

		targets = append(targets, switchTarget{Branch: entry.Branch, Path: entry.Path, Label: label})
	}

	return targets
}

// isSameOrInsidePath reports whether cwd is target or lives underneath it.
func isSameOrInsidePath(cwd, target string) bool {
	if cwd == "" || target == "" {
		return false
	}
	rel, err := filepath.Rel(target, cwd)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

func navigateToWorktree(cmd *cobra.Command, branch, path string) error {
	if isJSONOutput() {
		return emitJSONSuccess(cmd, map[string]any{
			"status":      "exists",
			"branch":      branch,
			"path":        path,
			"navigate_to": path,
		})
	}

	// Print a status line before the marker: every navigating command does, and
	// the shell wrapper's anchored grep can miss a marker on the first line when
	// script(1) prefixes it with a control character.
	fmt.Printf("✓ Switched to worktree: %s\n", path)
	printCDMarker(path)
	return nil
}

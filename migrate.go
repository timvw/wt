package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

type migrateAction string

const (
	migrateActionMove      migrateAction = "move"
	migrateActionMoveForce migrateAction = "move-force"
	migrateActionSkip      migrateAction = "skip"
)

type parsedWorktree struct {
	Path     string
	Branch   string
	Detached bool
	Main     bool
}

type migrateItem struct {
	Branch  string
	From    string
	To      string
	Primary bool
	Action  migrateAction
	Reason  string
}

type targetState int

const (
	targetMissing targetState = iota
	targetFile
	targetDirEmpty
	targetDirNonEmpty
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate existing worktrees to configured paths",
	Long: `Migrate existing linked worktrees to the currently configured location strategy.

If the primary checkout lives under WORKTREE_ROOT, it is moved back under ~/src.

Examples:
  wt migrate
  wt migrate --force`,
	RunE: func(cmd *cobra.Command, args []string) error {
		entries, err := listParsedWorktrees()
		if err != nil {
			return err
		}

		plan, err := buildMigratePlan(entries, migrateForce)
		if err != nil {
			return err
		}

		return applyMigratePlan(cmd, plan)
	},
}

func listParsedWorktrees() ([]parsedWorktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %w", err)
	}

	var entries []parsedWorktree
	var current *parsedWorktree

	flush := func() {
		if current == nil {
			return
		}
		entries = append(entries, *current)
		current = nil
	}

	for _, raw := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			flush()
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			flush()
			current = &parsedWorktree{Path: strings.TrimPrefix(line, "worktree ")}
			continue
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(line, "branch ") {
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
			continue
		}

		if line == "detached" {
			current.Detached = true
		}
	}

	flush()

	if len(entries) > 0 {
		entries[0].Main = true
	}

	return entries, nil
}

func buildMigratePlan(entries []parsedWorktree, force bool) ([]migrateItem, error) {
	info, err := getRepoInfo()
	if err != nil {
		return nil, err
	}

	var plan []migrateItem

	absWorktreeRoot, err := filepath.Abs(worktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve WORKTREE_ROOT: %w", err)
	}
	absWorktreeRoot = filepath.Clean(absWorktreeRoot)
	primaryTarget := resolvePrimaryCheckoutTarget(info)

	for _, wt := range entries {
		from := filepath.Clean(wt.Path)
		branchLabel := strings.TrimSpace(wt.Branch)
		if branchLabel == "" {
			branchLabel = "<detached>"
		}

		if wt.Main {
			if !isPathWithinRoot(from, absWorktreeRoot) {
				plan = append(plan, migrateItem{
					Branch:  branchLabel,
					From:    from,
					Primary: true,
					Action:  migrateActionSkip,
					Reason:  "primary checkout already outside WORKTREE_ROOT",
				})
				continue
			}

			to := filepath.Clean(primaryTarget)
			if from == to {
				plan = append(plan, migrateItem{
					Branch:  branchLabel,
					From:    from,
					To:      to,
					Primary: true,
					Action:  migrateActionSkip,
					Reason:  "primary checkout already at target path",
				})
				continue
			}

			state, err := detectTargetState(to)
			if err != nil {
				return nil, err
			}

			item := migrateItem{
				Branch:  branchLabel,
				From:    from,
				To:      to,
				Primary: true,
				Action:  migrateActionMove,
			}

			switch state {
			case targetMissing:
				// move
			case targetDirEmpty:
				item.Reason = "target path exists but is empty"
			case targetDirNonEmpty:
				if force {
					item.Action = migrateActionMoveForce
					item.Reason = "target path exists and is non-empty (force)"
				} else {
					item.Action = migrateActionSkip
					item.Reason = "target path exists and is non-empty"
				}
			case targetFile:
				if force {
					item.Action = migrateActionMoveForce
					item.Reason = "target path exists as file (force)"
				} else {
					item.Action = migrateActionSkip
					item.Reason = "target path exists as file"
				}
			}

			plan = append(plan, item)
			continue
		}

		if wt.Detached || strings.TrimSpace(wt.Branch) == "" {
			plan = append(plan, migrateItem{
				Branch: branchLabel,
				From:   from,
				To:     "",
				Action: migrateActionSkip,
				Reason: "detached or branchless worktree",
			})
			continue
		}

		targetPath, err := renderWorktreePath(info, wt.Branch)
		if err != nil {
			return nil, err
		}

		to := filepath.Clean(targetPath)

		if from == to {
			plan = append(plan, migrateItem{
				Branch: wt.Branch,
				From:   from,
				To:     to,
				Action: migrateActionSkip,
				Reason: "already in configured path",
			})
			continue
		}

		state, err := detectTargetState(to)
		if err != nil {
			return nil, err
		}

		item := migrateItem{
			Branch: branchLabel,
			From:   from,
			To:     to,
			Action: migrateActionMove,
		}

		switch state {
		case targetMissing:
			// move
		case targetDirEmpty:
			item.Reason = "target path exists but is empty"
		case targetDirNonEmpty:
			if force {
				item.Action = migrateActionMoveForce
				item.Reason = "target path exists and is non-empty (force)"
			} else {
				item.Action = migrateActionSkip
				item.Reason = "target path exists and is non-empty"
			}
		case targetFile:
			if force {
				item.Action = migrateActionMoveForce
				item.Reason = "target path exists as file (force)"
			} else {
				item.Action = migrateActionSkip
				item.Reason = "target path exists as file"
			}
		}

		plan = append(plan, item)
	}

	return plan, nil
}

func detectTargetState(target string) (targetState, error) {
	info, err := os.Stat(target)
	switch {
	case os.IsNotExist(err):
		return targetMissing, nil
	case err != nil:
		return targetMissing, fmt.Errorf("failed to stat target path %s: %w", target, err)
	}

	if !info.IsDir() {
		return targetFile, nil
	}

	empty, err := isDirEmpty(target)
	if err != nil {
		return targetMissing, fmt.Errorf("failed to inspect target path %s: %w", target, err)
	}
	if empty {
		return targetDirEmpty, nil
	}

	return targetDirNonEmpty, nil
}

func applyMigratePlan(cmd *cobra.Command, plan []migrateItem) error {
	jsonMode := isJSONOutput()
	moveCount := 0
	skipCount := 0
	failCount := 0
	results := make([]map[string]any, 0, len(plan))
	var primaryItems []migrateItem
	var secondaryItems []migrateItem

	record := func(item migrateItem, status, reason string) {
		result := map[string]any{
			"branch":  item.Branch,
			"from":    item.From,
			"status":  status,
			"primary": item.Primary,
		}
		if item.To != "" {
			result["to"] = item.To
		}
		if reason != "" {
			result["reason"] = reason
		}
		results = append(results, result)
	}

	for _, item := range plan {
		if item.Primary {
			primaryItems = append(primaryItems, item)
			continue
		}
		secondaryItems = append(secondaryItems, item)
	}

	for _, item := range primaryItems {
		switch item.Action {
		case migrateActionSkip:
			if !jsonMode {
				fmt.Printf("Skipped primary checkout: %s\n", item.Reason)
			}
			skipCount++
			record(item, "skipped", item.Reason)
		case migrateActionMove, migrateActionMoveForce:
			force := item.Action == migrateActionMoveForce
			if err := movePrimaryCheckout(item.From, item.To, force); err != nil {
				if !jsonMode {
					fmt.Printf("Failed primary checkout: %v\n", err)
				}
				failCount++
				record(item, "failed", err.Error())
				continue
			}
			if !jsonMode {
				fmt.Printf("Moved primary checkout: %s -> %s\n", item.From, item.To)
			}
			moveCount++
			record(item, "moved", item.Reason)
		}
	}

	for _, item := range secondaryItems {
		switch item.Action {
		case migrateActionSkip:
			if !jsonMode {
				fmt.Printf("Skipped %s: %s\n", item.Branch, item.Reason)
			}
			skipCount++
			record(item, "skipped", item.Reason)
			continue
		case migrateActionMove, migrateActionMoveForce:
			force := item.Action == migrateActionMoveForce
			if err := prepareMigrateTarget(item.To, force); err != nil {
				if !jsonMode {
					fmt.Printf("Failed %s: %v\n", item.Branch, err)
				}
				failCount++
				record(item, "failed", err.Error())
				continue
			}

			cmd := exec.Command("git", "worktree", "move", item.From, item.To)
			if !jsonMode {
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
			}
			if err := cmd.Run(); err != nil {
				if !jsonMode {
					fmt.Printf("Failed %s: %v\n", item.Branch, err)
				}
				failCount++
				record(item, "failed", err.Error())
				continue
			}

			if !jsonMode {
				fmt.Printf("Moved %s: %s -> %s\n", item.Branch, item.From, item.To)
			}
			moveCount++
			record(item, "moved", item.Reason)
		}
	}

	if jsonMode {
		if failCount == 0 {
			return emitJSONSuccess(cmd, map[string]any{
				"force":    migrateForce,
				"total":    len(plan),
				"migrated": moveCount,
				"skipped":  skipCount,
				"failed":   failCount,
				"results":  results,
			})
		}
		return fmt.Errorf("migration completed with %d failures", failCount)
	}

	fmt.Printf("\nMigration complete: %d moved, %d skipped, %d failed\n", moveCount, skipCount, failCount)
	if failCount > 0 {
		return fmt.Errorf("migration completed with %d failures", failCount)
	}

	return nil
}

func movePrimaryCheckout(from, to string, force bool) error {
	if err := prepareMigrateTarget(to, force); err != nil {
		return err
	}

	if err := os.Rename(from, to); err != nil {
		return fmt.Errorf("failed to move primary checkout from %s to %s: %w", from, to, err)
	}

	repairCmd := exec.Command("git", "-C", to, "worktree", "repair")
	if output, err := repairCmd.CombinedOutput(); err != nil {
		trimmed := strings.TrimSpace(string(output))
		if trimmed != "" {
			return fmt.Errorf("failed to repair worktrees after moving primary checkout: %v (%s)", err, trimmed)
		}
		return fmt.Errorf("failed to repair worktrees after moving primary checkout: %w", err)
	}

	return nil
}

func resolvePrimaryCheckoutTarget(info repoInfo) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("src", info.Name)
	}

	srcRoot := filepath.Join(home, "src")
	owner := strings.Trim(info.Owner, "/")
	if owner == "" {
		return filepath.Join(srcRoot, info.Name)
	}

	return filepath.Join(srcRoot, filepath.FromSlash(owner), info.Name)
}

func isPathWithinRoot(path, root string) bool {
	cleanPath := canonicalExistingPath(path)
	cleanRoot := canonicalExistingPath(root)

	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}

	if rel == "." {
		return true
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func canonicalExistingPath(path string) string {
	abs := path
	if absolute, err := filepath.Abs(path); err == nil {
		abs = absolute
	}

	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}

	return filepath.Clean(abs)
}

func prepareMigrateTarget(target string, force bool) error {
	state, err := detectTargetState(target)
	if err != nil {
		return err
	}

	switch state {
	case targetMissing:
		// nothing to remove
	case targetDirEmpty:
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("failed to remove empty target path %s: %w", target, err)
		}
	case targetDirNonEmpty:
		if !force {
			return fmt.Errorf("target path %s exists and is non-empty", target)
		}
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("failed to remove target path %s: %w", target, err)
		}
	case targetFile:
		if !force {
			return fmt.Errorf("target path %s exists as file", target)
		}
		if err := os.Remove(target); err != nil {
			return fmt.Errorf("failed to remove target file %s: %w", target, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("failed to create target parent directory for %s: %w", target, err)
	}

	return nil
}

package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

var migrateForce bool

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
			if primaryTarget == "" {
				// Skipped rather than fatal: the other worktrees still have
				// somewhere to go, and this one has nowhere wt is willing to
				// name. See resolvePrimaryCheckoutTarget.
				plan = append(plan, migrateItem{
					Branch:  branchLabel,
					From:    from,
					Primary: true,
					Action:  migrateActionSkip,
					Reason:  "origin URL does not resolve to a path under repo_root",
				})
				continue
			}
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

			// The primary checkout's destination does not come from the pattern,
			// so renderWorktreePath never sees it — but it is still built from
			// the repository: resolvePrimaryCheckoutTarget joins the owner and
			// name parsed out of the origin URL, and filepath.Join cleans, so an
			// owner of "x/../../.config" reaches ~/.config/wt from ~/src. Moving
			// a checkout onto wt's own state is the same hole as creating a
			// worktree there, and gets the same answer.
			if owned := wtStateAtPath(to); owned != "" {
				return nil, fmt.Errorf(
					"the primary checkout would be moved to %s, which is %s.\n"+
						"That path comes from this repository's origin URL, and the files moved there\n"+
						"would become wt's own config file and approval store, which is what decides\n"+
						"whether this repository's hooks run",
					to, owned)
			}

			reason, err := migrateTrustGain(from, to)
			if err != nil {
				return nil, err
			}
			if reason != "" {
				plan = append(plan, migrateItem{
					Branch:  branchLabel,
					From:    from,
					To:      to,
					Primary: true,
					Action:  migrateActionSkip,
					Reason:  reason,
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
			if err := refuseMoveOntoWtState(item.To); err != nil {
				if !jsonMode {
					fmt.Printf("Failed primary checkout: %v\n", err)
				}
				failCount++
				record(item, "failed", err.Error())
				continue
			}
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
			if err := refuseMoveOntoWtState(item.To); err != nil {
				if !jsonMode {
					fmt.Printf("Failed %s: %v\n", item.Branch, err)
				}
				failCount++
				record(item, "failed", err.Error())
				continue
			}
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

// resolvePrimaryCheckoutTarget returns where the primary checkout belongs, or ""
// when the origin URL does not name a path that stays under repo_root.
//
// Owner and Name are parsed out of the origin remote, which is a URL somebody
// handed the user, and filepath.Join cleans as it joins: an owner of
// "x/../../.config" with a name of "wt" resolves to ~/.config/wt rather than to
// anything under repo_root. That is a directory whose contents decide whether this
// repository's hooks run, and the move puts the repository's committed files
// there. wt clone refuses the same fields for the same reason — see
// repoPlacementPath — and migrate reads them from a repository already on disk.
func resolvePrimaryCheckoutTarget(info repoInfo) string {
	owner := strings.Trim(info.Owner, "/")
	if hasDotDotSegment(owner) || hasDotDotSegment(info.Name) {
		return ""
	}

	repoRoot, err := filepath.Abs(reposRoot)
	if err != nil {
		return ""
	}
	repoRoot = filepath.Clean(repoRoot)

	var target string
	if owner == "" {
		target = filepath.Join(repoRoot, info.Name)
	} else {
		target = filepath.Join(repoRoot, filepath.FromSlash(owner), info.Name)
	}
	if !isPathWithinRoot(target, repoRoot) {
		return ""
	}
	return target
}

// migrateTrustGain reports why moving a primary checkout would hand it trust it
// does not already have, or "" when it would not.
//
// Trust is pinned to where the primary sits: a [trust] rule matches its path,
// and a stored approval is keyed on its .git. Neither is normally the
// repository's to choose — except here. The destination is
// repo_root/{owner}/{name} built from the origin URL with the host dropped, so a clone of
// https://evil.example/acme/tool asks to be put exactly where github.com/acme/tool
// would go. Two different things can be waiting at that path:
//
//   - a [trust] rule, written for the tree the user keeps their own repositories
//     in; and
//   - an approval left behind by a repository that used to sit there. Current
//     records also carry a filesystem identity, so a replacement would not
//     inherit one; the destination does not exist yet, however, so migrate
//     cannot compare that identity before deciding where to move. Conservatively
//     decline instead of moving first and asking after the fact.
//
// Only a gain counts. A repository already sitting inside a whitelisted tree, or
// already carrying an approval, loses nothing by being tidied within it.
// Each side is asked the question it is allowed to get wrong. The destination
// does not exist yet, so nothing has settled its case and Win32 has not yet
// dropped the trailing dot from a name like "tool." — an origin URL ending
// "tool..git" renders a destination that compares equal to no rule and no
// record, and is then created as the "tool" both of them name. So the
// destination is asked loosely and the source strictly: a destination that only
// might be covered is worth a skip, a source that only might be covered is not
// worth waiving one. See samePath.
func migrateTrustGain(from, to string) (string, error) {
	covered, ok := trustWhitelistCovers(to)
	if !ok {
		return cannotTellReason(to), nil
	}
	if covered && !trustWhitelistAllows(from) {
		return fmt.Sprintf(
			"%s is covered by a [trust] rule, so moving it there would run its hooks unasked — "+
				"and that path comes from the origin URL, not from you. Move it yourself if you meant to",
			to), nil
	}

	store, err := loadTrustStore()
	if err != nil {
		// Not knowing what is approved decides whether a repository is moved
		// somewhere it would be pre-approved, so it stops the migration rather
		// than being read as "nothing is".
		return "", err
	}
	// Compared as sets of command hashes rather than as "is anything approved
	// here". A path can hold several approvals — worktrees of one repository sit
	// on branches whose .wt.toml differ, and each is kept — and asking only
	// whether the source has one lets an approval the user gave to an earlier
	// version of these hooks stand in for a different set waiting at the
	// destination. What counts is a set the destination answers to and the
	// current path does not.
	waiting, ok := approvedHashesAt(store, to, mayBeScopedUnder)
	if !ok {
		return cannotTellReason(to), nil
	}
	held, ok := approvedHashesAt(store, from, scopedUnder)
	if !ok {
		return cannotTellReason(from), nil
	}
	for sha := range waiting {
		if !held[sha] {
			displayPath := displayText(to)
			return fmt.Sprintf(
				"%s still carries a hook approval from a repository that used to be there, so moving it "+
					"there would run this repository's hooks unasked if the commands match — and that path "+
					"comes from the origin URL, not from you. Run wt untrust --path %s first, or move it "+
					"yourself if you meant to",
				displayPath, shellQuoteArgument(to)), nil
		}
	}
	return "", nil
}

// shellQuoteArgument renders the exact argument for the shell wt integrates
// with: POSIX-style shells on Unix and Git Bash, PowerShell otherwise on
// Windows. The explanatory copy of the path is passed through displayText by
// the caller; this copy must retain every byte so the suggested command still
// names paths containing whitespace, quotes, or newlines.
func shellQuoteArgument(arg string) string {
	if runtime.GOOS == "windows" && os.Getenv("MSYSTEM") == "" && os.Getenv("SHELL") == "" {
		return shellQuotePowerShell(arg)
	}
	return shellQuotePOSIX(arg)
}

func shellQuotePOSIX(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func shellQuotePowerShell(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "''") + "'"
}

// cannotTellReason is the skip for a path wt could not settle. Not knowing what
// a path names is not the same as knowing nothing is approved at it.
func cannotTellReason(path string) string {
	return fmt.Sprintf(
		"%s is reached through symlinks wt cannot follow to an end, so it cannot tell which approvals "+
			"already cover it. Move it yourself if you meant to",
		path)
}

// approvedHashesAt returns the command sets a primary checkout at path would
// already be approved for, whether or not anything is there now — its own, and
// those of the submodules scoped beneath its .git. under decides which records
// belong to a repository there; callers pick it to suit which way their answer
// is safe to be wrong. The second result is false when wt could not settle what
// the path names, which is not the same answer as an empty set.
func approvedHashesAt(store trustStore, path string, under func(scope, root string) bool) (map[string]bool, bool) {
	want, ok := canonicalExistingPath(filepath.Join(path, ".git"))
	if !ok {
		return nil, false
	}
	// The working tree root as well as its .git, because not every scope is a
	// git directory: where git would not confirm which repository a working tree
	// belongs to, defaultRepoTrustKey pins the approval to the directory's own
	// path instead. Such a record answers for commands that run at a checkout
	// here, and asking only beneath .git never sees it — <path> is not under
	// <path>/.git. Resolved separately rather than assumed to nest, since a .git
	// may be a symlink pointing somewhere else entirely.
	root, ok := canonicalExistingPath(path)
	if !ok {
		return nil, false
	}
	found := map[string]bool{}
	for _, rec := range store.Trusted {
		if rec.Scope == "" || rec.Scope == trustScopeUser {
			continue
		}
		// A record wt cannot resolve is not evidence about this path — and it is
		// the attacker who needs a record to match, never to hide.
		if got, ok := canonicalExistingPath(rec.Scope); ok && (under(got, want) || under(got, root)) {
			found[rec.SHA256] = true
		}
	}
	return found, true
}

// refuseMoveOntoWtState asks what a destination is at the moment of the move,
// rather than trusting the answer from when the plan was drawn.
//
// The plan is built against the filesystem as it stands, and then the moves
// change it. The primary goes first and materialises everything it had
// committed, so a `link -> ../../../.config` in that repository is a name that
// resolves to nothing while the plan is drawn and a live symlink by the time the
// linked worktrees move. A pattern pointing through it therefore passed a check
// that was true when it ran and false when it mattered. Ask again, here.
func refuseMoveOntoWtState(to string) error {
	owned := wtStateAtPath(to)
	if owned == "" {
		return nil
	}
	return fmt.Errorf(
		"refusing to move onto %s, which is %s.\n"+
			"  The files moved there would become wt's own config file and approval store,\n"+
			"  which is what decides whether this repository's hooks run",
		to, owned)
}

func isPathWithinRoot(path, root string) bool {
	cleanPath, pathOK := canonicalExistingPath(path)
	cleanRoot, rootOK := canonicalExistingPath(root)
	if !pathOK || !rootOK {
		// The one caller skips the worktree when this is false, which is the
		// side to be on when what the path names could not be settled.
		return false
	}

	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return false
	}

	if rel == "." {
		return true
	}

	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// canonicalExistingPath resolves the symlinks in the part of path that exists
// and keeps the rest as written.
//
// filepath.EvalSymlinks gives up on the whole path when any component is
// missing, and both callers routinely hand it one that is: a migration target
// has not been created yet, and wtStateAtPath guards a config directory that on
// a fresh machine is not there either. Resolving nothing in that case would let
// a ~/.config symlinked into a dotfiles repo compare unequal to the path the
// files actually arrive at.
//
// A symlink whose target does not exist is followed by hand, because backing
// off past it treats its name as an ordinary missing directory. It is not one:
// the name stands for the target, and creating the target is exactly what makes
// the link live. A ~/.config/wt pointing into a dotfiles repo that has not been
// cloned yet is the ordinary way to have one, and a repository that names the
// target as its worktree pattern would populate wt's config directory while
// comparing equal to nothing.
//
// Bounded, because two dangling links can name each other. Running out of hops
// reports failure rather than the path as it stands: a real chain is nowhere
// near this long, so exhausting the budget means wt has not established which
// directory the path names. Both callers guard something, and an unresolved path
// compares equal to nothing — which is the guard passing, not holding.
func canonicalExistingPath(path string) (string, bool) {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	path = filepath.Clean(path)

	for hops := 0; hops < 32; hops++ {
		resolved, rest, dangling := splitAtResolvable(path)
		if dangling == "" {
			return filepath.Join(resolved, rest), true
		}
		path = filepath.Clean(filepath.Join(dangling, rest))
	}
	return "", false
}

// splitAtResolvable walks path upwards until EvalSymlinks accepts a prefix, and
// returns that resolved prefix with the components below it. If the walk steps
// onto a symlink whose target is missing, it returns the target instead, for
// canonicalExistingPath to carry on from.
func splitAtResolvable(path string) (resolved, rest, dangling string) {
	for {
		if r, err := filepath.EvalSymlinks(path); err == nil {
			return r, rest, ""
		}
		if target, err := os.Readlink(path); err == nil {
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(path), target)
			}
			return "", rest, target
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path, rest, ""
		}
		rest = filepath.Join(filepath.Base(path), rest)
		path = parent
	}
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

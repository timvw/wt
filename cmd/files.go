package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/timvw/wt/internal/fileops"
	"github.com/timvw/wt/internal/ignore"
)

// worktreeIncludeFile is the de-facto standard name for a repo-level list of
// untracked paths that belong in every worktree. worktrunk, gtr and Claude
// Code's worktree feature all read it, so a repo that adds one gets working
// behaviour from all of them at once — which is why wt adopts the name rather
// than inventing a wt-specific one.
const worktreeIncludeFile = ".worktreeinclude"

// vcsDirs are never copied, whatever the configuration says. Copying a nested
// repository's metadata into a worktree produces a broken checkout, and
// copying .git itself would be actively destructive.
var vcsDirs = map[string]bool{
	".git":   true,
	".bzr":   true,
	".hg":    true,
	".jj":    true,
	".pijul": true,
	".sl":    true,
	".svn":   true,
}

// File actions reported in text and JSON output. The set is closed: agents
// match on it, so adding a value is an interface change.
const (
	fileActionCopied  = "copied"
	fileActionLinked  = "linked"
	fileActionSkipped = "skipped"
	fileActionFailed  = "failed"
)

// progressFileThreshold and progressByteThreshold are the point past which a
// copy announces itself on stderr. Below them the operation is fast enough
// that a progress line is just noise; above them, silence looks like a hang.
const (
	progressFileThreshold = 1000
	progressByteThreshold = 1 << 30 // 1 GiB
)

// fileConfig is the effective [files] configuration for one repository, with
// the .worktreeinclude layer folded in.
type fileConfig struct {
	Copy        []layeredPattern
	Link        []layeredPattern
	Exclude     []layeredPattern
	CopyIgnored bool

	// IncludeFilePath is where .worktreeinclude was looked for, and
	// IncludeFileFound whether it was there.
	IncludeFilePath  string
	IncludeFileFound bool

	copyMatcher     *ignore.Matcher
	copyDenyMatcher *ignore.Matcher
	excludeMatcher  *ignore.Matcher
}

// configured reports whether anything is set up that could produce work.
func (c fileConfig) configured() bool {
	return c.CopyIgnored || len(c.Copy) > 0 || len(c.Link) > 0
}

// noCopyFiles is the --no-copy flag shared by create/checkout/pr/mr. One
// variable is enough because exactly one of those runs per invocation.
var noCopyFiles bool

// copyOptions carries the per-invocation switches of `wt copy`.
type copyOptions struct {
	DryRun bool
	Force  bool
}

// fileResult describes what happened to one path.
type fileResult struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Method string `json:"method,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
	Target string `json:"target,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// copySummary is the compact tally embedded in create/checkout/pr/mr payloads.
type copySummary struct {
	Copied    int `json:"copied"`
	Reflinked int `json:"reflinked"`
	Linked    int `json:"linked"`
	Skipped   int `json:"skipped"`
	Failed    int `json:"failed"`
}

// empty reports whether the copy did nothing at all.
func (s copySummary) empty() bool {
	return s.Copied == 0 && s.Linked == 0 && s.Skipped == 0 && s.Failed == 0
}

// copyResult is the full record of one copy run.
type copyResult struct {
	Source      string       `json:"source"`
	Destination string       `json:"destination"`
	DryRun      bool         `json:"dry_run"`
	Summary     copySummary  `json:"summary"`
	Files       []fileResult `json:"files"`
}

// filesDisabled reports whether file materialisation is switched off for this
// invocation. It parallels WT_HOOKS_DISABLED.
func filesDisabled() bool {
	return os.Getenv("WT_FILES_DISABLED") == "1"
}

// resolveFileConfig folds .worktreeinclude into the accumulated config layers
// and compiles the matchers.
//
// The include file is read here rather than in loadWorktreeConfig because it
// lives at the *main* worktree root, and finding that costs a git call that
// every unrelated `wt` command would otherwise pay for.
func resolveFileConfig(mainWorktree string) (fileConfig, error) {
	cfg := fileConfig{
		Copy:        append([]layeredPattern(nil), filesCopy...),
		Link:        append([]layeredPattern(nil), filesLink...),
		Exclude:     append([]layeredPattern(nil), filesExclude...),
		CopyIgnored: filesCopyIgnored,
	}

	if mainWorktree != "" {
		cfg.IncludeFilePath = filepath.Join(mainWorktree, worktreeIncludeFile)
		// F7: .worktreeinclude is committed, so a hostile repo could ship it as
		// a symlink to ~/.ssh/config and have wt read a file outside the
		// worktree — and print its lines back through `wt info`. Lstat first
		// and insist on a regular file.
		info, err := os.Lstat(cfg.IncludeFilePath)
		switch {
		case err == nil && !info.Mode().IsRegular():
			return cfg, fmt.Errorf("%s is not a regular file; refusing to follow it", cfg.IncludeFilePath)
		case err == nil:
			f, openErr := os.Open(cfg.IncludeFilePath)
			if openErr != nil {
				return cfg, fmt.Errorf("failed to read %s: %w", cfg.IncludeFilePath, openErr)
			}
			cfg.IncludeFileFound = true
			patterns, parseErr := ignore.ParseFile(f)
			_ = f.Close()
			if parseErr != nil {
				return cfg, fmt.Errorf("failed to read %s: %w", cfg.IncludeFilePath, parseErr)
			}
			cfg.Copy = accumulateFilePatterns(cfg.Copy, patterns, worktreeIncludeFile)
		case errors.Is(err, os.ErrNotExist):
			// Normal: most repos have no .worktreeinclude.
		default:
			return cfg, fmt.Errorf("failed to read %s: %w", cfg.IncludeFilePath, err)
		}
	}

	if err := validateFilePatterns(cfg); err != nil {
		return cfg, err
	}

	// A "!" in copy is a deny, not a rule a later layer can argue with, so the
	// negations are compiled into a matcher of their own and applied last.
	// Leaving them in the copy matcher would make them last-match-wins: a
	// committed .worktreeinclude naming cache/private.key would undo the user's
	// own "!cache/private.key", which is precisely the hole that rejecting "!"
	// in exclude closes.
	positive, denied := splitCopyNegations(cfg.Copy)

	var err error
	if cfg.copyMatcher, err = ignore.New(positive); err != nil {
		return cfg, fmt.Errorf("invalid [files] copy pattern: %w", err)
	}
	if cfg.copyDenyMatcher, err = ignore.New(denied); err != nil {
		return cfg, fmt.Errorf("invalid [files] copy pattern: %w", err)
	}
	if cfg.excludeMatcher, err = ignore.New(patternStrings(cfg.Exclude)); err != nil {
		return cfg, fmt.Errorf("invalid [files] exclude pattern: %w", err)
	}
	return cfg, nil
}

// splitCopyNegations separates the copy list into the patterns that select and
// the "!" patterns that deny, with the "!" stripped from the latter.
func splitCopyNegations(patterns []layeredPattern) (positive, denied []string) {
	for _, p := range patterns {
		if rest, found := strings.CutPrefix(p.Pattern, "!"); found {
			denied = append(denied, rest)
			continue
		}
		positive = append(positive, p.Pattern)
	}
	return positive, denied
}

func patternStrings(patterns []layeredPattern) []string {
	out := make([]string, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, p.Pattern)
	}
	return out
}

// validateFilePatterns enforces invariants F1 and F3: no pattern may name
// anything outside the main worktree.
//
// A leading "/" is *not* an absolute path here — in gitignore syntax it anchors
// the pattern to the root, so "/.env" means ".env at the top level" and stays
// allowed. What is rejected is a ".." segment, which is the only way a pattern
// could climb out, and a Windows drive- or UNC-absolute path, which has no
// gitignore meaning at all.
//
// link entries are stricter still: they are literal paths rather than globs,
// so a leading "/" really would be absolute and is refused.
func validateFilePatterns(cfg fileConfig) error {
	for _, group := range []struct {
		key      string
		patterns []layeredPattern
		literal  bool
		// noNegation marks a list where "!" would let a later layer undo an
		// earlier one, which the accumulation rules do not allow.
		noNegation bool
	}{
		{key: "copy", patterns: cfg.Copy},
		{key: "exclude", patterns: cfg.Exclude, noNegation: true},
		{key: "link", patterns: cfg.Link, literal: true, noNegation: true},
	} {
		for _, p := range group.patterns {
			if group.noNegation && strings.HasPrefix(p.Pattern, "!") {
				// Excludes accumulate with the repo's .wt.toml applied after the
				// user's own config, so a committed "!*.pem" would re-include
				// exactly what the user's global exclude was protecting.
				return fmt.Errorf("[files] %s pattern %q (from %s) is a negation; %s is applied last and cannot be re-included",
					group.key, p.Pattern, p.Source, group.key)
			}
			body := strings.TrimPrefix(p.Pattern, "!")
			if hasDotDotSegment(body) {
				return fmt.Errorf("[files] %s pattern %q (from %s) contains a %q path segment; patterns must stay inside the worktree",
					group.key, p.Pattern, p.Source, "..")
			}
			if isAbsoluteFilePattern(body, group.literal) {
				return fmt.Errorf("[files] %s pattern %q (from %s) is an absolute path; patterns are relative to the worktree root",
					group.key, p.Pattern, p.Source)
			}
		}
	}
	return nil
}

// isAbsoluteFilePattern reports whether a pattern names a location outside any
// worktree. literal marks entries (link) where a leading separator is absolute
// rather than a gitignore anchor.
func isAbsoluteFilePattern(p string, literal bool) bool {
	if p == "" {
		return false
	}
	if strings.HasPrefix(p, `\\`) || filepath.VolumeName(p) != "" {
		// UNC share or a "C:" drive letter.
		return true
	}
	if literal && (p[0] == '/' || p[0] == '\\') {
		return true
	}
	return false
}

// dirIsSafeToCreate reports whether dst/rel can be made a real directory.
//
// noSymlinkComponents deliberately stops before the leaf, which is right for a
// file (O_EXCL handles it) but not for a directory: MkdirAll and Chmod both
// follow a symlink standing where the directory should be, so a worktree with a
// tracked "cache -> /outside" would have wt chmod something outside it (F7).
func dirIsSafeToCreate(dst, rel string) bool {
	if !noSymlinkComponents(dst, rel) {
		return false
	}
	info, err := os.Lstat(filepath.Join(dst, filepath.FromSlash(rel)))
	if err != nil {
		// Nothing there yet, so MkdirAll will create a real directory.
		return os.IsNotExist(err)
	}
	// Lstat, so a symlink to a directory reports false — which is the point.
	return info.IsDir()
}

// noSymlinkComponents reports whether every existing directory component
// between root and root/rel is a real directory.
//
// withinRoot is lexical, which is not enough on its own: a worktree can contain
// a *tracked* symlink, so a destination like "cache/secret.txt" passes the
// lexical check and still writes to wherever "cache" points. Refusing to
// traverse a symlinked component is what actually holds F2 and F7 — see
// TestSecurityF2SymlinkedDestinationParentIsRefused.
//
// Only the components are checked, not the leaf: a symlink at the leaf is the
// no-clobber case, which O_EXCL and the "exists" skip already handle.
func noSymlinkComponents(root, rel string) bool {
	segments := strings.Split(filepath.ToSlash(rel), "/")
	current := root
	for _, seg := range segments[:max(len(segments)-1, 0)] {
		if seg == "" || seg == "." {
			continue
		}
		current = filepath.Join(current, seg)
		info, err := os.Lstat(current)
		if err != nil {
			// Missing components are created by EnsureParent as real
			// directories, so there is nothing to traverse.
			if os.IsNotExist(err) {
				return true
			}
			return false
		}
		if !info.IsDir() {
			// A symlink (whatever it points at) or a file where a directory is
			// expected. Either way, do not write through it.
			return false
		}
	}
	return true
}

// withinRoot reports whether path is a strict descendant of root. It is the
// check behind invariants F1, F2 and F7: everything read and written has to
// resolve inside the source or destination worktree.
func withinRoot(root, path string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// listIgnoredCandidates asks git which paths under root are untracked and
// ignored.
//
// Delegating to git rather than reimplementing gitignore is what makes nested
// .gitignore files, core.excludesFile and .git/info/exclude all work, and it
// satisfies invariant F5 by construction: a tracked file is never in the
// answer, so a worktree's checked-out content can never be overwritten with
// the main worktree's working copy.
//
// --directory collapses a wholly-ignored directory into one entry, so a
// 40,000-file node_modules costs one line here and is only walked if it
// survives filtering.
func listIgnoredCandidates(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root,
		"ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "--full-name", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list ignored files in %s: %w", root, err)
	}

	var candidates []string
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry != "" {
			candidates = append(candidates, entry)
		}
	}
	return candidates, nil
}

// isTrackedPath reports whether rel names anything git tracks in root, either
// as a file or as a directory containing tracked files.
//
// The copy side gets this for free because its candidates come from
// ls-files --others; link entries are named literally, so they need the check.
// Link lists are short, so one git call per entry is cheaper than listing the
// whole index.
func isTrackedPath(root, rel string) bool {
	// ":(literal)" stops git reading the path as a pathspec glob, so a link
	// entry named "a[1].txt" asks about that file rather than a character class.
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--", ":(literal)"+rel)
	out, err := cmd.Output()
	if err != nil {
		// A path outside the repo, or git failing for any other reason, is not
		// evidence that the path is tracked; the caller's other guards apply.
		return false
	}
	return len(strings.TrimRight(string(out), "\x00")) > 0
}

// registeredWorktreePaths returns every worktree git knows about, as absolute
// cleaned paths.
//
// This is what stops the inside-dotdir strategy from eating itself: with
// worktrees at <repo>/.worktrees/<branch>, ".worktrees" is ignored, so it is a
// copy candidate, and copying it would recursively duplicate every worktree
// into the new one.
func registeredWorktreePaths() map[string]bool {
	paths := map[string]bool{}
	entries, err := getWorktreeListPorcelain()
	if err != nil {
		return paths
	}
	for _, e := range entries {
		if e.Path == "" {
			continue
		}
		if abs, err := filepath.Abs(e.Path); err == nil {
			paths[filepath.Clean(abs)] = true
		}
	}
	return paths
}

// copyPlan is the set of paths a run will act on, resolved before anything is
// written so that --dry-run and the real run agree exactly.
type copyPlan struct {
	// dirs are directories to create even if empty, in the order discovered.
	dirs []string
	// files are the slash-separated relative paths to materialise.
	files []string
	// bytes is the total size of files, used to decide whether to show
	// progress.
	bytes int64
	// failures are paths that could not even be inspected.
	failures []fileResult
}

// planner walks the source worktree and decides what to copy.
type planner struct {
	src           string
	cfg           fileConfig
	worktreePaths map[string]bool
	plan          copyPlan
}

// buildCopyPlan resolves the configuration into a concrete list of paths.
func buildCopyPlan(src string, cfg fileConfig) (copyPlan, error) {
	p := &planner{src: src, cfg: cfg, worktreePaths: registeredWorktreePaths()}

	candidates, err := listIgnoredCandidates(src)
	if err != nil {
		return copyPlan{}, err
	}

	for _, candidate := range candidates {
		rel := strings.TrimSuffix(candidate, "/")
		isDir := strings.HasSuffix(candidate, "/")
		if rel == "" || p.skip(rel, filepath.Base(rel)) {
			continue
		}
		if p.excluded(rel, isDir) {
			continue
		}

		matched := p.selected(rel, isDir, false)
		if !isDir {
			if matched {
				p.addFile(rel)
			}
			continue
		}
		if !matched && !cfg.copyMatcher.MayContain(rel) {
			continue
		}
		if matched {
			p.plan.dirs = append(p.plan.dirs, rel)
		}
		p.walk(rel, matched)
	}

	sort.Strings(p.plan.files)
	sort.Strings(p.plan.dirs)
	return p.plan, nil
}

// walk descends into a directory. forced means an ancestor already matched, so
// everything below it is included without consulting the copy patterns again —
// which is how "copy = [\"node_modules\"]" pulls in the whole tree.
func (p *planner) walk(rel string, forced bool) {
	dir := filepath.Join(p.src, filepath.FromSlash(rel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		p.plan.failures = append(p.plan.failures, fileResult{
			Path:   rel,
			Action: fileActionFailed,
			Reason: err.Error(),
		})
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		childRel := rel + "/" + name
		if p.skip(childRel, name) {
			continue
		}
		// A symlink to a directory reports IsDir() == false, so it is handled
		// as a file and recreated as a symlink rather than descended into
		// (invariant F4).
		isDir := entry.IsDir()
		if p.excluded(childRel, isDir) {
			continue
		}

		matched := p.selected(childRel, isDir, forced)
		if !isDir {
			if matched {
				p.addFile(childRel)
			}
			continue
		}
		if !matched && !p.cfg.copyMatcher.MayContain(childRel) {
			continue
		}
		if matched {
			p.plan.dirs = append(p.plan.dirs, childRel)
		}
		p.walk(childRel, matched)
	}
}

// skip applies the hard-coded exclusions: VCS metadata and any path that is
// itself a registered worktree of this repository.
func (p *planner) skip(rel, name string) bool {
	if vcsDirs[name] {
		return true
	}
	abs := filepath.Join(p.src, filepath.FromSlash(rel))
	if absClean, err := filepath.Abs(abs); err == nil {
		if p.worktreePaths[filepath.Clean(absClean)] {
			return true
		}
	}
	// F1/F7: never read anything the source worktree does not contain.
	return !withinRoot(p.src, abs)
}

// excluded reports whether rel is kept out, by the exclude list or by a "!" in
// copy. Both are final: no later layer and no blanket yes can undo either.
//
// Any decision at all counts, not just Selected: a "!" in exclude is rejected
// when the config is loaded, so a Rejected here would mean that check was
// bypassed — and an exclude that can be argued out of is not an exclude.
func (p *planner) excluded(rel string, isDir bool) bool {
	if p.cfg.copyDenyMatcher.Decide(rel, isDir) != ignore.Unmatched {
		return true
	}
	return p.cfg.excludeMatcher.Decide(rel, isDir) != ignore.Unmatched
}

// selected reports whether rel belongs in the plan.
//
// forced carries down from a directory that was already selected, and
// copy_ignored selects everything git reports as ignored. Both are overridden
// by an explicit "!" in copy: a negation is the user naming a path they do not
// want, and that has to win over a blanket yes.
func (p *planner) selected(rel string, isDir, forced bool) bool {
	switch p.cfg.copyMatcher.Decide(rel, isDir) {
	case ignore.Rejected:
		return false
	case ignore.Selected:
		return true
	default:
		return forced || p.cfg.CopyIgnored
	}
}

func (p *planner) addFile(rel string) {
	p.plan.files = append(p.plan.files, rel)
	if info, err := os.Lstat(filepath.Join(p.src, filepath.FromSlash(rel))); err == nil {
		p.plan.bytes += info.Size()
	}
}

// runFileCopy materialises the configured files from src into dst.
//
// It never removes the destination worktree or returns a partial-failure error:
// an unreadable source file is reported as a failed entry, not as a reason to
// abandon the run.
func runFileCopy(src, dst string, opts copyOptions) (*copyResult, error) {
	if filesDisabled() {
		return &copyResult{Source: src, Destination: dst, DryRun: opts.DryRun}, nil
	}

	cfg, err := resolveFileConfig(src)
	if err != nil {
		return nil, err
	}
	result := &copyResult{Source: src, Destination: dst, DryRun: opts.DryRun}
	if !cfg.configured() {
		return result, nil
	}

	plan, err := buildCopyPlan(src, cfg)
	if err != nil {
		return nil, err
	}

	results := append([]fileResult(nil), plan.failures...)
	copied := copyPlannedFiles(src, dst, plan, opts)
	results = append(results, copied...)
	// The real run copies before it links, so a link whose destination the copy
	// stage has just claimed is skipped as existing. A dry run writes nothing,
	// so it has to be told what the copy stage would have produced.
	results = append(results, linkConfiguredPaths(src, dst, cfg, opts, claimedPaths(dst, opts, copied))...)

	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	result.Files = results
	result.Summary = summarise(results)
	return result, nil
}

// copyPlannedFiles executes the file half of a plan through a bounded worker
// pool. Reflink is a metadata operation and parallelises well; the buffered
// fallback benefits from the overlap too.
func copyPlannedFiles(src, dst string, plan copyPlan, opts copyOptions) []fileResult {
	// Before the empty-plan shortcut: a matched directory is created even when
	// it holds no files at all, which is the entire content of a plan that
	// selects an empty cache/ or an empty node_modules/.
	dirFailures := createPlannedDirs(src, dst, plan.dirs, opts.DryRun)

	if len(plan.files) == 0 {
		return dirFailures
	}

	files, collisions := dropCaseCollisions(dst, plan.files)
	results := make([]fileResult, len(files))

	if opts.DryRun {
		for i, rel := range files {
			results[i] = dryRunResult(src, dst, rel, opts.Force)
		}
		results = append(results, collisions...)
		return append(results, dirFailures...)
	}

	progress := newProgressReporter(len(files), plan.bytes)
	workers := min(8, runtime.NumCPU())
	if workers < 1 {
		workers = 1
	}

	var (
		wg   sync.WaitGroup
		next atomic.Int64
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(files) {
					return
				}
				results[i] = copyOne(src, dst, files[i], opts.Force)
				progress.done()
			}
		}()
	}
	wg.Wait()
	progress.finish()

	results = append(results, collisions...)
	return append(results, dirFailures...)
}

// createPlannedDirs materialises the directories a plan selected in their own
// right and reports every one it refuses or cannot create. Discarding those
// errors would let `wt copy` say "nothing to copy" for a run whose entire
// content was a directory that never appeared.
//
// A dry run makes the two refusals that can be checked without writing, and
// says nothing about the rest: an mkdir that will fail on permissions cannot be
// predicted without attempting it.
func createPlannedDirs(src, dst string, dirs []string, dryRun bool) []fileResult {
	var failures []fileResult
	for _, rel := range dirs {
		dir := filepath.Join(dst, filepath.FromSlash(rel))
		switch {
		case !withinRoot(dst, dir):
			failures = append(failures, fileResult{Path: rel, Action: fileActionFailed, Reason: "path escapes the worktree"})
		case !noSymlinkComponents(dst, rel):
			failures = append(failures, fileResult{Path: rel, Action: fileActionFailed, Reason: "destination parent is a symlink"})
		case !dirIsSafeToCreate(dst, rel):
			failures = append(failures, fileResult{Path: rel, Action: fileActionFailed, Reason: "destination exists and is not a directory"})
		case dryRun:
			// Nothing to create.
		default:
			if err := fileops.MkdirAllFrom(dir, filepath.Join(src, filepath.FromSlash(rel))); err != nil {
				failures = append(failures, fileResult{Path: rel, Action: fileActionFailed, Reason: err.Error()})
			}
		}
	}
	return failures
}

// copyOne materialises a single relative path.
func copyOne(src, dst, rel string, force bool) fileResult {
	srcPath := filepath.Join(src, filepath.FromSlash(rel))
	dstPath := filepath.Join(dst, filepath.FromSlash(rel))

	// F2/F7: refuse anything that would land outside the destination worktree,
	// lexically and through a symlinked parent.
	if !withinRoot(dst, dstPath) || !withinRoot(src, srcPath) {
		return fileResult{Path: rel, Action: fileActionFailed, Reason: "path escapes the worktree"}
	}
	if !noSymlinkComponents(dst, rel) {
		return fileResult{Path: rel, Action: fileActionFailed, Reason: "destination parent is a symlink"}
	}

	if err := fileops.EnsureParent(dstPath); err != nil {
		return fileResult{Path: rel, Action: fileActionFailed, Reason: err.Error()}
	}

	method, err := fileops.CopyFile(srcPath, dstPath)
	if errors.Is(err, os.ErrExist) {
		if !force {
			// F6: an existing destination is never overwritten silently.
			return fileResult{Path: rel, Action: fileActionSkipped, Reason: "exists"}
		}
		if info, statErr := os.Lstat(dstPath); statErr == nil && info.IsDir() {
			// Replacing a real directory with a file is beyond what --force
			// should be allowed to mean.
			return fileResult{Path: rel, Action: fileActionFailed, Reason: "destination is a directory"}
		}
		method, err = forceCopyOver(srcPath, dstPath)
	}
	if err != nil {
		return fileResult{Path: rel, Action: fileActionFailed, Reason: err.Error()}
	}

	res := fileResult{Path: rel, Action: fileActionCopied, Method: string(method)}
	if info, err := os.Lstat(dstPath); err == nil && info.Mode().IsRegular() {
		res.Bytes = info.Size()
	}
	return res
}

// forceCopyOver replaces an existing destination with a fresh copy of src.
//
// Removing the destination first and copying afterwards would be simpler, but
// it opens a window in which the old file is gone and the new one does not
// exist yet: an unreadable source or a full disk at that point costs the user a
// file that --force only promised to update. So copy beside it under a
// temporary name and rename over it once the copy has succeeded — a failure
// then costs nothing but the temporary.
func forceCopyOver(srcPath, dstPath string) (fileops.Method, error) {
	dir, base := filepath.Split(dstPath)

	// CreateTemp only reserves the name: CopyFile insists on creating the
	// destination itself (O_EXCL is the no-clobber guarantee) and may have to
	// create it as a symlink rather than a file.
	tmp, err := os.CreateTemp(dir, base+".wt-tmp-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	if err := os.Remove(tmpPath); err != nil {
		return "", err
	}
	// Leaves nothing behind on any of the failure paths below; after a
	// successful rename there is nothing left to remove.
	defer func() { _ = os.Remove(tmpPath) }()

	method, err := fileops.CopyFile(srcPath, tmpPath)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return "", err
	}
	return method, nil
}

// dryRunResult predicts what copyOne would do, without touching anything.
func dryRunResult(src, dst, rel string, force bool) fileResult {
	dstPath := filepath.Join(dst, filepath.FromSlash(rel))
	srcPath := filepath.Join(src, filepath.FromSlash(rel))

	// The refusals copyOne makes have to be predicted here too, or --dry-run
	// promises a copy the real run will decline.
	if !withinRoot(dst, dstPath) || !withinRoot(src, srcPath) {
		return fileResult{Path: rel, Action: fileActionFailed, Reason: "path escapes the worktree"}
	}
	if !noSymlinkComponents(dst, rel) {
		return fileResult{Path: rel, Action: fileActionFailed, Reason: "destination parent is a symlink"}
	}

	// --force is part of what is being previewed: without it an existing
	// destination is skipped, with it the copy goes ahead unless the
	// destination is a directory, which --force is not allowed to replace.
	if existing, err := os.Lstat(dstPath); err == nil {
		switch {
		case !force:
			return fileResult{Path: rel, Action: fileActionSkipped, Reason: "exists"}
		case existing.IsDir():
			return fileResult{Path: rel, Action: fileActionFailed, Reason: "destination is a directory"}
		}
	}

	res := fileResult{Path: rel, Action: fileActionCopied, Method: string(fileops.MethodCopy)}
	info, err := os.Lstat(srcPath)
	if err != nil {
		return fileResult{Path: rel, Action: fileActionFailed, Reason: err.Error()}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		res.Method = string(fileops.MethodSymlink)
		return res
	}
	// CopyFile refuses anything that is neither a regular file nor a symlink,
	// so a FIFO, socket or device node reached through a selected directory has
	// to be previewed as the failure it will be rather than as a copy.
	if !info.Mode().IsRegular() {
		return fileResult{
			Path:   rel,
			Action: fileActionFailed,
			Reason: fmt.Sprintf("unsupported file type %s: %s", info.Mode().Type(), srcPath),
		}
	}
	res.Bytes = info.Size()
	if reflinkAvailable(src, dst) {
		res.Method = string(fileops.MethodReflink)
	}
	return res
}

// reflinkAvailable probes whether a clone between these two trees would work,
// so --dry-run reports the method the real run will actually use. The probe is
// cached per source/destination pair because it costs two syscalls.
var (
	reflinkProbeMu    sync.Mutex
	reflinkProbeCache = map[string]bool{}
)

func reflinkAvailable(src, dst string) bool {
	key := src + "\x00" + dst
	reflinkProbeMu.Lock()
	defer reflinkProbeMu.Unlock()
	if cached, ok := reflinkProbeCache[key]; ok {
		return cached
	}

	ok := false
	// A clone cannot span filesystems, so an answer is free in that case — and
	// checking first keeps the probe off cross-device pairs entirely.
	if fileops.SameFilesystem(src, dst) {
		// Both probe files go in the destination. It is on the same filesystem
		// as the source, so the answer is the same, and the source worktree —
		// the user's actual working copy, possibly read-only — is never
		// written to, not even by `wt copy --dry-run`.
		if probe, err := os.CreateTemp(dst, ".wt-reflink-probe-"); err == nil {
			name := probe.Name()
			_ = probe.Close()
			clone := name + ".clone"
			ok = fileops.Reflink(name, clone) == nil
			_ = os.Remove(name)
			_ = os.Remove(clone)
		}
	}
	reflinkProbeCache[key] = ok
	return ok
}

// caseInsensitiveWorktree reports whether dst is on a case-insensitive
// filesystem, without writing anything anywhere.
//
// filesystemCaseInsensitive answers the same question by creating a temp file.
// That is fine where it is already used, but not here: this runs on the
// --dry-run path via dropCaseCollisions, and "show what would happen, change
// nothing" has to mean it. So take a name that already exists — an entry in
// dst, or failing that dst itself and its ancestors — flip its case, and ask
// whether the two names lead to the same file.
func caseInsensitiveWorktree(dst string) bool {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return false
	}

	if entries, err := os.ReadDir(dst); err == nil {
		for _, entry := range entries {
			if answer, ok := sameFileWithFlippedCase(filepath.Join(dst, entry.Name())); ok {
				return answer
			}
		}
	}
	// An empty or unreadable directory still has a path of its own, and dst
	// shares a filesystem with itself.
	for path := filepath.Clean(dst); ; {
		if answer, ok := sameFileWithFlippedCase(path); ok {
			return answer
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	// Nothing along the way held a letter to flip. Both platforms that reach
	// this line are case-insensitive by default, and guessing that way costs a
	// reported collision rather than a silent overwrite.
	return true
}

// sameFileWithFlippedCase reports whether path and the same path with the case
// of its last element flipped are one and the same file. ok is false when the
// name holds no cased letter, or when the answer cannot be established.
func sameFileWithFlippedCase(path string) (result, ok bool) {
	dir, name := filepath.Split(path)
	alt := strings.ToUpper(name)
	if alt == name {
		alt = strings.ToLower(name)
	}
	if alt == name {
		// Digits and punctuation only; nothing to learn from this one.
		return false, false
	}

	original, err := os.Lstat(path)
	if err != nil {
		return false, false
	}
	flipped, err := os.Lstat(filepath.Join(dir, alt))
	if err != nil {
		// A missing flipped name settles it; anything else leaves the question
		// open for the next candidate.
		return false, os.IsNotExist(err)
	}
	// Both names may exist as distinct files on a case-sensitive filesystem, so
	// identity is the question rather than existence.
	return os.SameFile(original, flipped), true
}

// dropCaseCollisions removes paths that would collide on a case-insensitive
// filesystem, reporting them rather than letting one silently land on top of
// the other.
func dropCaseCollisions(dst string, files []string) ([]string, []fileResult) {
	if !caseInsensitiveWorktree(dst) {
		return files, nil
	}

	seen := make(map[string]string, len(files))
	kept := make([]string, 0, len(files))
	var dropped []fileResult
	for _, rel := range files {
		lower := strings.ToLower(rel)
		if first, clash := seen[lower]; clash {
			dropped = append(dropped, fileResult{
				Path:   rel,
				Action: fileActionSkipped,
				Reason: fmt.Sprintf("case-insensitive collision with %s", first),
			})
			continue
		}
		seen[lower] = rel
		kept = append(kept, rel)
	}
	return kept, dropped
}

// claimedPaths reports which destination paths a dry run's copy stage would
// have produced, so the link stage sees them the way the real run does: there,
// copying happens first and the link then finds its destination taken. Outside
// a dry run the filesystem answers this by itself and the predicate is never
// true.
func claimedPaths(dst string, opts copyOptions, copied []fileResult) func(string) bool {
	if !opts.DryRun {
		return func(string) bool { return false }
	}

	// A destination that differs only by case is the same destination here, so
	// the lookup has to fold exactly as dropCaseCollisions does.
	fold := caseInsensitiveWorktree(dst)
	key := func(rel string) string {
		if fold {
			return strings.ToLower(rel)
		}
		return rel
	}

	claimed := make(map[string]bool, len(copied))
	for _, r := range copied {
		if r.Action == fileActionCopied {
			claimed[key(r.Path)] = true
		}
	}
	return func(rel string) bool { return claimed[key(rel)] }
}

// linkConfiguredPaths creates the symlinks named by [files] link.
//
// Semantics differ from copy on purpose: entries are literal relative paths
// rather than globs (globbing a symlink target is ambiguous and nobody wants
// it), a missing source is a warning rather than an error (a node_modules that
// has not been installed yet must not break `wt create`), and an existing
// destination is never replaced even with --force (swapping a real directory
// for a symlink is too destructive to do on a flag).
func linkConfiguredPaths(src, dst string, cfg fileConfig, opts copyOptions, claimed func(string) bool) []fileResult {
	var results []fileResult
	for _, entry := range cfg.Link {
		// Link entries name a path rather than a glob, so resolve gitignore's
		// escaping (trailing spaces, "\" escapes) into the literal name.
		rel := filepath.ToSlash(strings.Trim(ignore.LiteralPath(entry.Pattern), "/"))
		if rel == "" {
			continue
		}

		srcPath := filepath.Join(src, filepath.FromSlash(rel))
		dstPath := filepath.Join(dst, filepath.FromSlash(rel))
		if !withinRoot(src, srcPath) || !withinRoot(dst, dstPath) {
			results = append(results, fileResult{Path: rel, Action: fileActionFailed, Reason: "path escapes the worktree"})
			continue
		}
		// F2/F7: a symlinked parent in either worktree would make the lexical
		// check above meaningless — refuse to traverse one.
		if !noSymlinkComponents(src, rel) || !noSymlinkComponents(dst, rel) {
			results = append(results, fileResult{Path: rel, Action: fileActionFailed, Reason: "parent directory is a symlink"})
			continue
		}

		// The real directory bit decides whether a "cache/" exclude applies, so
		// the source has to be stat-ed before the exclude list is consulted.
		info, statErr := os.Lstat(srcPath)

		// exclude is applied last and cannot be overridden — that holds for
		// link just as it does for copy, otherwise "*.key" in exclude would
		// still be honoured for copies and silently ignored for links.
		if cfg.excludeMatcher.Decide(rel, statErr == nil && info.IsDir()) != ignore.Unmatched {
			results = append(results, fileResult{Path: rel, Action: fileActionSkipped, Reason: "excluded"})
			continue
		}

		// F5: link entries are named literally rather than drawn from the
		// ignored-candidate list, so this is where tracked paths are kept out.
		if isTrackedPath(src, rel) {
			results = append(results, fileResult{Path: rel, Action: fileActionSkipped, Reason: "tracked by git"})
			continue
		}

		if statErr != nil {
			results = append(results, fileResult{Path: rel, Action: fileActionSkipped, Reason: "source does not exist"})
			continue
		}
		if _, err := os.Lstat(dstPath); err == nil || claimed(rel) {
			results = append(results, fileResult{Path: rel, Action: fileActionSkipped, Reason: "exists"})
			continue
		}

		if opts.DryRun {
			results = append(results, fileResult{
				Path:   rel,
				Action: fileActionLinked,
				Method: string(fileops.MethodSymlink),
				Target: srcPath,
			})
			continue
		}

		if err := fileops.EnsureParent(dstPath); err != nil {
			results = append(results, fileResult{Path: rel, Action: fileActionFailed, Reason: err.Error()})
			continue
		}
		if err := os.Symlink(srcPath, dstPath); err != nil {
			results = append(results, fileResult{Path: rel, Action: fileActionFailed, Reason: err.Error()})
			continue
		}
		results = append(results, fileResult{
			Path:   rel,
			Action: fileActionLinked,
			Method: string(fileops.MethodSymlink),
			Target: srcPath,
		})
	}
	return results
}

func summarise(results []fileResult) copySummary {
	var s copySummary
	for _, r := range results {
		switch r.Action {
		case fileActionCopied:
			s.Copied++
			if r.Method == string(fileops.MethodReflink) {
				s.Reflinked++
			}
		case fileActionLinked:
			s.Linked++
		case fileActionSkipped:
			s.Skipped++
		case fileActionFailed:
			s.Failed++
		}
	}
	return s
}

// progressReporter announces long copies on stderr so a multi-gigabyte
// node_modules does not look like a hang. It stays silent below the
// thresholds, and in JSON mode, where stdout must stay parseable and stderr
// noise is not wanted either.
type progressReporter struct {
	total   int
	enabled bool
	count   atomic.Int64
}

func newProgressReporter(total int, bytes int64) *progressReporter {
	p := &progressReporter{total: total}
	p.enabled = !isJSONOutput() && (total > progressFileThreshold || bytes > progressByteThreshold)
	if p.enabled {
		fmt.Fprintf(os.Stderr, "Copying %d files (%s)...\n", total, humanBytes(bytes))
	}
	return p
}

func (p *progressReporter) done() {
	if !p.enabled {
		return
	}
	if n := p.count.Add(1); n%500 == 0 {
		fmt.Fprintf(os.Stderr, "  %d/%d files\n", n, p.total)
	}
}

func (p *progressReporter) finish() {
	if p.enabled {
		fmt.Fprintf(os.Stderr, "  %d/%d files\n", p.count.Load(), p.total)
	}
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// materialiseFiles runs the automatic copy for create/checkout/pr/mr.
//
// Failure is deliberately non-fatal and never rolls back the worktree, matching
// how post_create hook failures are already handled: a missing .env should not
// cost you a worktree.
func materialiseFiles(info repoInfo, worktreePath string, skip bool) *copySummary {
	if skip || filesDisabled() {
		return nil
	}
	if info.Main == "" || info.Main == worktreePath {
		return nil
	}

	result, err := runFileCopy(info.Main, worktreePath, copyOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠ file copy failed: %v\n", err)
		return nil
	}
	if result == nil || result.Summary.empty() {
		return nil
	}
	if !isJSONOutput() {
		fmt.Printf("  %s\n", formatCopySummary(result.Summary))
	}
	return &result.Summary
}

// formatCopySummary renders the one-line tally shown after a worktree is
// created.
func formatCopySummary(s copySummary) string {
	parts := []string{}
	if s.Copied > 0 {
		if s.Reflinked > 0 {
			parts = append(parts, fmt.Sprintf("Copied %s (%d reflinked)", pluralFiles(s.Copied), s.Reflinked))
		} else {
			parts = append(parts, fmt.Sprintf("Copied %s", pluralFiles(s.Copied)))
		}
	}
	if s.Linked > 0 {
		parts = append(parts, fmt.Sprintf("linked %d", s.Linked))
	}
	if s.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d", s.Skipped))
	}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("failed %d", s.Failed))
	}
	if len(parts) == 0 {
		return "Nothing to copy"
	}
	return strings.Join(parts, ", ")
}

func pluralFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

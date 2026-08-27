package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/timvw/wt/internal/tmpl"
)

func buildWorktreePath(info repoInfo, branch string) (string, error) {
	rendered, err := renderWorktreePath(info, branch)
	if err != nil {
		return "", err
	}

	parent := filepath.Dir(rendered)
	infoStat, err := os.Stat(parent)
	switch {
	case err == nil:
		if !infoStat.IsDir() {
			return "", fmt.Errorf("worktree path %s is not a directory", parent)
		}
	case os.IsNotExist(err):
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return "", fmt.Errorf("failed to create worktree directory %s: %w", parent, err)
		}
	default:
		return "", fmt.Errorf("failed to access worktree directory %s: %w", parent, err)
	}

	return rendered, nil
}

func renderWorktreePath(info repoInfo, branch string) (string, error) {
	pattern, err := resolveWorktreePattern()
	if err != nil {
		return "", err
	}

	sep := worktreeSeparator

	context := map[string]any{
		"repo": repoInfo{
			Main:  info.Main,
			Host:  info.Host,
			Owner: tmpl.Transform(sep, info.Owner),
			Name:  info.Name,
		},
		"branch":       strings.TrimSpace(tmpl.Transform(sep, branch)),
		"worktreeRoot": worktreeRoot,
		// Context rules are matched against the repository's main checkout, not
		// the working directory. Every linked worktree of a repo then resolves
		// to the same rule, which the cwd would not: a repo cloned under
		// repo_root keeps its worktrees in a different tree entirely.
		"env": templateEnv(sep, info.Main),
	}

	if pattern == "" {
		return "", fmt.Errorf("worktree pattern cannot be empty")
	}

	rendered, err := tmpl.Render(pattern, context, sep)
	if err != nil {
		return "", fmt.Errorf("pattern variables missing values: %w", err)
	}
	rendered = filepath.FromSlash(rendered)
	if !filepath.IsAbs(rendered) {
		rendered = filepath.Join(worktreeRoot, rendered)
	}

	rendered = filepath.Clean(rendered)
	if owned := wtStateAtPath(rendered); owned != "" {
		return "", fmt.Errorf(
			"this worktree would be created at %s, which is %s.\n"+
				"A repository may choose the pattern, so wt does not let the pattern choose that path:\n"+
				"the files checked out there would become wt's own config file and approval store,\n"+
				"which is what decides whether that repository's hooks run.\n"+
				"Change the pattern — it currently comes from %s",
			rendered, owned, patternSourceLabel())
	}
	return rendered, nil
}

// wtStateAtPath describes the wt-owned file or directory a worktree at path
// would land on top of, or "" when it lands somewhere harmless.
//
// A repository's .wt.toml may set the worktree pattern — that is project policy,
// and the whole point of the setting. It may not set `root`, so the tree the
// pattern is anchored in stays the user's; but a pattern that renders to an
// absolute path is not anchored anywhere, and "{.env.HOME}/.config/wt" names the
// directory holding config.toml and trust.toml.
//
// `git worktree add` then writes the repository's files there. That is not a
// hook running — the gate is not bypassed, it is *replaced*: the branch supplies
// a config.toml whose hooks carry user-config scope, and a trust.toml whose
// (scope, sha256) pair the attacker precomputed for them. Nothing was approved
// and nothing was prompted for, and it fires in every repository afterwards, not
// just this one. Refuse the placement instead, which is the only moment wt still
// gets a say.
//
// Both directions of containment, because the leaf is not the only way in: a
// worktree AT ~/.config plants ~/.config/wt/config.toml from a committed
// wt/config.toml just as well. git will not write into a non-empty directory, so
// in practice this needs the target not to exist yet — a fresh machine, which is
// exactly when nothing has been approved and the store is easiest to author.
func wtStateAtPath(path string) string {
	owned := []struct{ path, what string }{
		{configDir(), "where wt keeps its config file and its record of approved hooks"},
		{configFilePath, "your config file"},
	}
	path = canonicalExistingPath(path)
	for _, o := range owned {
		if o.path == "" || !filepath.IsAbs(o.path) {
			continue
		}
		against := canonicalExistingPath(o.path)
		switch {
		case samePathTree(path, against) && foldPath(path) == foldPath(against):
			return o.what
		case samePathTree(path, against):
			// Named, because the containing or contained case is the one where
			// the rendered path alone does not show what the collision is with.
			return fmt.Sprintf("%s (%s)", o.what, o.path)
		}
	}
	return ""
}

// samePathTree reports whether either path contains the other, comparing without
// regard to case.
//
// A byte comparison is not what the filesystem will do. macOS and Windows are
// case-insensitive by default, so "~/.config/WT" is the very directory
// configDir() names — a one-character edit to a pattern that walks straight past
// an exact match and lands on trust.toml all the same. Verified: it did.
//
// Folded on every platform rather than on darwin and windows, and the difference
// only ever refuses more. What gets refused is a path differing from wt's own
// config directory in nothing but case, which no pattern wants on purpose — so
// the cost on a case-sensitive filesystem is nil, and it covers the ones that
// turn up anyway: a casefolded ext4 directory, a mounted exFAT volume, a
// case-insensitive APFS volume on an otherwise case-sensitive machine.
//
// Case is the fold that is reachable without knowing anything about the machine.
// A macOS volume also equates the NFC and NFD spellings of a non-ASCII name,
// which this does not, and neither ".config" nor "wt" has one — reaching that
// would mean naming the user's home directory outright in a spelling they do not
// use, rather than reading it from {.env.HOME}.
func samePathTree(a, b string) bool {
	a, b = foldPath(a), foldPath(b)
	return hasPathPrefix(a, b) || hasPathPrefix(b, a)
}

func foldPath(p string) string { return strings.ToLower(p) }

// patternSourceLabel names the layer the worktree pattern came from, so the
// refusal above says whose setting to go and change.
func patternSourceLabel() string {
	if configSources.Pattern != "" {
		return configSources.Pattern
	}
	return "the worktree strategy"
}

func cleanupWorktreePath(worktreePath string) error {
	if worktreePath == "" {
		return nil
	}

	if err := os.RemoveAll(worktreePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove worktree directory %s: %w", worktreePath, err)
	}

	absRoot, err := filepath.Abs(worktreeRoot)
	if err != nil {
		return nil
	}

	absWorktreePath, err := filepath.Abs(worktreePath)
	if err != nil {
		return nil
	}

	repoDir := filepath.Dir(absWorktreePath)
	if strings.HasPrefix(repoDir, absRoot) {
		if empty, err := isDirEmpty(repoDir); err == nil && empty {
			_ = os.Remove(repoDir)
		}
	}

	return nil
}

func warnIfCaseInsensitivePathCollision(worktreePath string) {
	if isJSONOutput() || !filesystemCaseInsensitive(worktreePath) {
		return
	}

	if existingPath, ok := findCaseInsensitivePathCollision(worktreePath); ok {
		fmt.Fprintf(os.Stderr, "Warning: worktree path %s collides with existing path %s on this case-insensitive filesystem. Consider setting separator = \"-\" in your wt config or avoiding case-only branch names.\n", worktreePath, existingPath)
	}
}

func findCaseInsensitivePathCollision(path string) (string, bool) {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)

	current := volume
	if filepath.IsAbs(path) {
		current += string(os.PathSeparator)
		rest = strings.TrimPrefix(rest, string(os.PathSeparator))
	} else if current == "" {
		current = "."
	}

	for _, part := range strings.Split(rest, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}

		entries, err := os.ReadDir(current)
		if err != nil {
			return "", false
		}

		exactPath := filepath.Join(current, part)
		foundExact := false
		for _, entry := range entries {
			name := entry.Name()
			if name == part {
				foundExact = true
				break
			}
			if strings.EqualFold(name, part) {
				return filepath.Join(current, name), true
			}
		}
		if !foundExact {
			// The component is not in the listing under this spelling, and no
			// case variant matched either. On Windows it may still be a valid
			// 8.3 short name (RUNNER~1 for runneradmin), which os.ReadDir
			// reports only by its long name. Those resolve through Stat, so
			// keep walking rather than giving up on the rest of the path.
			if _, err := os.Stat(exactPath); err != nil {
				return "", false
			}
		}

		current = exactPath
	}

	return "", false
}

func filesystemCaseInsensitive(path string) bool {
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return false
	}

	dir := nearestExistingDir(path)
	if dir == "" {
		return runtime.GOOS == "windows"
	}

	file, err := os.CreateTemp(dir, ".wt-case-test-")
	if err != nil {
		return runtime.GOOS == "windows"
	}
	name := file.Name()
	_ = file.Close()
	defer func() { _ = os.Remove(name) }()

	altName := filepath.Join(dir, strings.ToUpper(filepath.Base(name)))
	if altName == name {
		altName = filepath.Join(dir, strings.ToLower(filepath.Base(name)))
	}
	if altName == name {
		return false
	}

	_, err = os.Stat(altName)
	return err == nil
}

func nearestExistingDir(path string) string {
	if path == "" {
		path = "."
	}

	path = filepath.Clean(path)
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return path
		}
		return filepath.Dir(path)
	}

	for {
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			return parent
		}
		path = parent
	}
}

func resolveWorktreePattern() (string, error) {
	if worktreePattern != "" {
		return worktreePattern, nil
	}
	if worktreeStrategy == "custom" {
		return "", fmt.Errorf("WORKTREE_PATTERN is required when WORKTREE_STRATEGY is 'custom'")
	}

	switch worktreeStrategy {
	case "global":
		return "{.worktreeRoot}/{.repo.Name}/{.branch}", nil
	case "sibling-repo", "sibling":
		return "{.repo.Main}/../{.repo.Name}-{.branch}", nil
	case "parent-worktrees", "parent-centered":
		return "{.repo.Main}/../{.repo.Name}.worktrees/{.branch}", nil
	case "parent-branches", "repo-root":
		return "{.repo.Main}/../{.branch}", nil
	case "parent-dotdir", "local-root":
		return "{.repo.Main}/../.worktrees/{.branch}", nil
	case "inside-dotdir", "nested-local":
		return "{.repo.Main}/.worktrees/{.branch}", nil
	default:
		return "", fmt.Errorf("unsupported WORKTREE_STRATEGY: %s", worktreeStrategy)
	}
}

func printCDMarker(path string) {
	if isJSONOutput() {
		return
	}
	fmt.Printf("wt navigating to: %s\n", path)
}

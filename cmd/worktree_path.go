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
		"env":          tmpl.EnvMap(sep),
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
	return rendered, nil
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

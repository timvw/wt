package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

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
// The answer when a path's symlinks cannot be followed to an end. Phrased to
// read after "which is", like the entries in the owned table: the callers all
// refuse on any non-empty answer, and "wt could not tell" has to be one of them.
const unfollowableChain = "reached through symlinks wt cannot follow to an end, " +
	"so it cannot tell whether that is its own config directory"

// gitConfigIsWhatRuns explains the second thing a worktree may not be placed on
// top of. Phrased to read after "which is", like the rest of the owned table.
const gitConfigIsWhatRuns = "where git keeps the configuration it applies to every repository, " +
	"which names programs git runs on its own (core.hooksPath, credential.helper, and the rest)"

// gitGlobalConfigPaths names the files and directories git reads as its global
// configuration.
//
// wt's gate is about hook commands, and core.hooksPath is a hook command by
// another name: a branch checked out over ~/.config/git supplies git's own
// config and a hooks directory alongside it, and the next `git worktree add` —
// wt's own, or any other — runs what is in it. Nothing was approved and nothing
// was prompted for, and like the trust store it fires in every repository
// afterwards rather than only in this one.
//
// The same reasoning as wt's own config directory, and the same limit: this
// covers the configuration of the program wt drives, not every program's
// dotfiles. A worktree pattern rendering an absolute path is otherwise the
// user's business — see docs/configuration.md.
func gitGlobalConfigPaths() []string {
	var paths []string
	// GIT_CONFIG_GLOBAL replaces both of the below when set. Only an absolute
	// value names a file to protect; the defaults are listed anyway, in case it
	// is ignored. A relative one is a hole wt cannot plug and did not open — see
	// warnRelativeGitConfigGlobal.
	if p := os.Getenv("GIT_CONFIG_GLOBAL"); filepath.IsAbs(p) {
		paths = append(paths, p)
	} else if strings.TrimSpace(p) != "" {
		warnRelativeGitConfigGlobal(p)
	}
	home, err := os.UserHomeDir()
	if err == nil && filepath.IsAbs(home) {
		paths = append(paths, filepath.Join(home, ".gitconfig"))
	}
	// The directory, not just the config file in it: a worktree AT
	// ~/.config/git plants a committed config there just as well, which is the
	// same reason wt guards its own config directory rather than only its file.
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if filepath.IsAbs(xdg) {
		paths = append(paths, filepath.Join(xdg, "git"))
	} else if err == nil && filepath.IsAbs(home) {
		paths = append(paths, filepath.Join(home, ".config", "git"))
	}
	return append(paths, gitGlobalIncludePaths()...)
}

// gitGlobalIncludePaths names the files git's global configuration pulls in by
// [include] and [includeIf].
//
// An included file is git's global configuration, spelled indirectly — and git
// ignores an include whose file is not there rather than complaining, so a
// `path = ~/dotfiles/gitconfig` on a machine where the dotfiles have not been
// cloned is an armed slot rather than a broken setting. A repository whose
// pattern renders onto ~/dotfiles fills it, and every git command afterwards
// reads what it committed. Guarding ~/.gitconfig and leaving what it includes
// open would be guarding the doorway and not the door.
//
// The conditions on an [includeIf] are not evaluated. Whether the file is read
// in THIS repository is not the question — it is read in whichever repository
// matches, and the placement is what puts the file there.
//
// Read from git rather than parsed here: the values are wanted before the files
// exist, and `git config --global` reports them either way.
func gitGlobalIncludePaths() []string {
	out, err := exec.Command("git", "config", "--global", "--null",
		"--get-regexp", `^include(if\..*)?\.path$`).Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, record := range strings.Split(string(out), "\x00") {
		_, value, hasValue := strings.Cut(record, "\n")
		if !hasValue {
			continue
		}
		// git resolves "~" here and takes a relative path against the config
		// file holding it. wt only guards what it can name from here, which is
		// the absolute ones — a relative include is a different file per config
		// file, and guessing which would be worse than saying nothing.
		if expanded := expandTilde(value); filepath.IsAbs(expanded) {
			paths = append(paths, expanded)
		}
	}
	return paths
}

// relativeGitConfigGlobalWarning keeps the notice to once per process.
var relativeGitConfigGlobalWarning sync.Once

// warnRelativeGitConfigGlobal says out loud that this PR's promise does not hold
// for this environment.
//
// git takes GIT_CONFIG_GLOBAL relative to the working directory, so a value like
// "gitconfig" means a different file in every repository — including one a
// repository committed, holding core.hooksPath. wt cannot close that: the file
// is read the moment any git command runs there, whatever wt does or refuses to
// do, and nothing wt does created it. But "nothing runs until you approve it" is
// silently untrue on such a machine, and the only wrong answer is to keep quiet.
func warnRelativeGitConfigGlobal(value string) {
	relativeGitConfigGlobalWarning.Do(func() {
		fmt.Fprintf(os.Stderr,
			"⚠ GIT_CONFIG_GLOBAL is set to %q, which is a relative path.\n"+
				"  git resolves it per directory, so a repository that commits a file by that name\n"+
				"  supplies your global git config — including core.hooksPath, which wt cannot gate.\n"+
				"  Set it to an absolute path.\n\n",
			value)
	})
}

// gitHookDirIsWhatRuns explains a placement onto the repository's own git
// directory. Phrased to read after "which is", like the rest of the owned table.
const gitHookDirIsWhatRuns = "inside this repository's own git directory, where git keeps the hooks " +
	"it runs for it — a worktree there is the repository writing its own .git"

// gitRepoOwnedPaths names the places the repository wt is standing in gets its
// hooks run from.
//
// `git worktree add` will check out into an existing directory as long as it is
// empty, and .git/hooks is empty on any clone made with no init template. A
// branch whose tree is a post-checkout file, placed there, is run by the very
// next `git worktree add` — wt's own — with the approval gate never consulted.
// Verified rather than reasoned about; it takes two commands.
//
// core.hooksPath for the same reason one step further out: it is where git will
// look instead, so a value naming a directory that does not exist yet is the
// same armed slot as an [include] pointing at absent dotfiles.
func gitRepoOwnedPaths() []string {
	var paths []string
	if dir, err := gitCommonDir(); err == nil && filepath.IsAbs(dir) {
		paths = append(paths, dir)
	}
	out, err := exec.Command("git", "config", "--get", "core.hooksPath").Output()
	if err != nil {
		return paths
	}
	// Relative to the top of the working tree, per git — which is the
	// repository's own directory, already covered by refusing to place a
	// worktree inside another. Only an absolute value names somewhere else.
	if p := expandTilde(strings.TrimSpace(string(out))); filepath.IsAbs(p) {
		paths = append(paths, p)
	}
	return paths
}

func wtStateAtPath(path string) string {
	owned := []struct{ path, what string }{
		{configDir(), "where wt keeps its config file and its record of approved hooks"},
		{configFilePath, "your config file"},
	}
	for _, p := range gitGlobalConfigPaths() {
		owned = append(owned, struct{ path, what string }{p, gitConfigIsWhatRuns})
	}
	for _, p := range gitRepoOwnedPaths() {
		owned = append(owned, struct{ path, what string }{p, gitHookDirIsWhatRuns})
	}
	path, ok := canonicalExistingPath(path)
	if !ok {
		return unfollowableChain
	}
	for _, o := range owned {
		if o.path == "" || !filepath.IsAbs(o.path) {
			continue
		}
		against, ok := canonicalExistingPath(o.path)
		if !ok {
			// wt's own directory is the unfollowable one. There is then nothing
			// to compare against, so there is no answer but "cannot tell".
			return unfollowableChain
		}
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
//
// And case is not the only spelling a filesystem folds. macOS firmlinks make
// /Users/alice and /System/Volumes/Data/Users/alice one directory — same device,
// same inode, and EvalSymlinks leaves both alone because neither is a symlink —
// so "/System/Volumes/Data{.env.HOME}/.config/wt" is a one-line detour around
// any comparison of names. A Linux bind mount aliases two paths the same way.
// Where the filesystem can answer, ask it: identity for the deepest part of each
// path that exists, names only for what is not there yet. Verified: it does.
func samePathTree(a, b string) bool {
	if hasPathPrefixFold(a, b) || hasPathPrefixFold(b, a) {
		return true
	}

	pa, aOK := splitAtExisting(a)
	pb, bOK := splitAtExisting(b)
	if !aOK || !bOK {
		return false
	}
	if os.SameFile(pa.info, pb.info) {
		// One directory, and both remainders are relative to it. Either being
		// empty means that side is the directory the other hangs off.
		return pa.tail == "" || pb.tail == "" ||
			hasPathPrefixFold(pa.tail, pb.tail) || hasPathPrefixFold(pb.tail, pa.tail)
	}
	// The two can exist to different depths, so neither base need be the other.
	// Only a path that exists in full can hold the other's base: if one's own
	// existence stopped higher up, it stopped at a component the other does not
	// have, and there they diverge rather than nest.
	return (pa.tail == "" && dirWithin(pb.base, pa.base)) ||
		(pb.tail == "" && dirWithin(pa.base, pb.base))
}

// pathParts is a path split where the filesystem stops: base is the deepest
// ancestor that exists, so the OS can be asked which directory it is, and tail
// is what below it is still only a name.
type pathParts struct {
	base string
	info os.FileInfo
	tail string
}

// splitAtExisting splits path at the deepest ancestor that exists.
//
// Something always does — a worktree is placed before it is created, and on a
// fresh machine wt's config directory has not been made either, but the home
// directory holding both is there. That is far enough down for identity to
// settle which directory each side is really talking about.
func splitAtExisting(path string) (pathParts, bool) {
	path = filepath.Clean(path)
	tail := ""
	for {
		if info, err := os.Stat(path); err == nil {
			return pathParts{base: path, info: info, tail: tail}, true
		}
		parent := filepath.Dir(path)
		if parent == path {
			return pathParts{}, false
		}
		tail = filepath.Join(filepath.Base(path), tail)
		path = parent
	}
}

// dirWithin reports whether dir is root, or sits under it, asking the
// filesystem which directory each ancestor is rather than what it is called.
func dirWithin(dir, root string) bool {
	target, err := os.Stat(root)
	if err != nil {
		return false
	}
	for p := filepath.Clean(dir); ; {
		if info, err := os.Stat(p); err == nil && os.SameFile(info, target) {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// hasPathPrefixFold is hasPathPrefix without regard to case. See samePathTree
// for why the fold is unconditional rather than per-GOOS.
func hasPathPrefixFold(path, prefix string) bool {
	return hasPathPrefix(foldPath(path), foldPath(prefix))
}

// foldPath spells a path the way the filesystem will read it, so that two paths
// naming one file compare equal.
//
// Case is one fold; on Windows, trailing dots and spaces are another. Only the
// components that do not exist yet need this — Stat resolves the ones that do,
// and identity settles those — but those are the ones that matter here, since a
// worktree is placed before it is created.
func foldPath(p string) string {
	p = strings.ToLower(p)
	if runtime.GOOS == "windows" {
		p = trimWindowsPathComponents(p)
	}
	return p
}

// trimWindowsPathComponents drops the trailing dots and spaces Win32 drops from
// every path component: a repository asking for "{.env.APPDATA}/wt." is asking
// for %APPDATA%\wt, which is where wt keeps config.toml and trust.toml, while
// comparing equal to nothing.
//
// A component of nothing but dots is left alone: "." and ".." are not names
// with trailing dots, they are the two relative components.
//
// Both separators, because this runs on a rendered pattern: a .wt.toml writes
// its paths with forward slashes and Windows accepts them.
func trimWindowsPathComponents(p string) string {
	var out strings.Builder
	out.Grow(len(p))
	start := 0
	for i := 0; i <= len(p); i++ {
		if i < len(p) && p[i] != '/' && p[i] != '\\' {
			continue
		}
		component := p[start:i]
		if trimmed := strings.TrimRight(component, ". "); trimmed != "" {
			component = trimmed
		}
		out.WriteString(component)
		if i < len(p) {
			out.WriteByte(p[i])
		}
		start = i + 1
	}
	return out.String()
}

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

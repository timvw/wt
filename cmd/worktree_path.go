package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
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
	// A pattern committed in .wt.toml is content the repository supplied. Keep
	// it inside the root the user selected, even when it renders as an absolute
	// path: otherwise cloning a repository also lets it choose any empty path on
	// the machine for the next checkout. Machine-local layers remain free to
	// place worktrees elsewhere; those are settings the user controls.
	if configSources.Pattern == hookSourceRepoConfig && !isPathWithinRoot(rendered, worktreeRoot) {
		return "", fmt.Errorf(
			"repository pattern would place this worktree at %s, outside the configured worktree root %s.\n"+
				"Patterns from .wt.toml are confined to that root; set wt.pattern in local git config or WORKTREE_PATTERN if you intend this placement",
			rendered, worktreeRoot)
	}
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
	// is ignored. A value that does not name one directory is a hole wt cannot
	// plug and did not open — see warnRelativeGitEnv.
	if p := os.Getenv("GIT_CONFIG_GLOBAL"); namesOneDirectory(p) {
		paths = append(paths, p)
	} else if strings.TrimSpace(p) != "" {
		// Including /proc/self/cwd/.gitconfig, which is absolute and is the
		// repository's own file: guarding a path that already means "here" would
		// be refusing to place a worktree on something the repository has
		// already supplied.
		warnRelativeGitEnv("GIT_CONFIG_GLOBAL", p)
	}
	// The system file too. /etc/gitconfig is root's and not placeable, but
	// GIT_CONFIG_SYSTEM redirects it, and a value under the user's own home is
	// as fillable as any other — the settings it carries are the same ones.
	if p := os.Getenv("GIT_CONFIG_SYSTEM"); namesOneDirectory(p) {
		paths = append(paths, p)
	} else if strings.TrimSpace(p) != "" {
		warnRelativeGitEnv("GIT_CONFIG_SYSTEM", p)
	}
	// namesOneDirectory here too, and warned about: HOME=/proc/self/cwd has git
	// reading the repository's own .gitconfig as your global one — core.hooksPath
	// and all — while wt guards a ~/.gitconfig that means "here" and so means
	// nothing. The message wt already prints for having no config directory says
	// approvals cannot be stored; it does not say git is being configured by the
	// checkout, and that is the half that runs commands.
	home, err := os.UserHomeDir()
	homeNamesOneDirectory := err == nil && namesOneDirectory(home)
	if homeNamesOneDirectory {
		paths = append(paths, filepath.Join(home, ".gitconfig"))
	} else if err == nil && strings.TrimSpace(home) != "" {
		warnRelativeGitEnv(homeEnvName(), home)
	}
	// The directory, not just the config file in it: a worktree AT
	// ~/.config/git plants a committed config there just as well, which is the
	// same reason wt guards its own config directory rather than only its file.
	xdg := os.Getenv("XDG_CONFIG_HOME")
	switch {
	case namesOneDirectory(xdg):
		// The file as well as the directory. Guarding a directory says nothing
		// about where a symlink inside it points, and ~/.config/git/config is as
		// often a link into a dotfiles repository as trust.toml is — a dangling
		// one being a path a pattern can render onto. ~/.gitconfig is named as a
		// file already and needs no equivalent.
		paths = append(paths, filepath.Join(xdg, "git"), filepath.Join(xdg, "git", "config"))
	default:
		// A relative one is the GIT_CONFIG_GLOBAL hole under another name, and
		// worse for being quiet: wt ignores a non-absolute XDG_CONFIG_HOME per
		// the XDG spec and falls back to ~/.config, while git honours it against
		// the working directory. Verified — with XDG_CONFIG_HOME=.xdg, git reads
		// a committed .xdg/git/config, core.hooksPath and all. Same answer as
		// there: no placement to refuse, so say it out loud.
		//
		if strings.TrimSpace(xdg) != "" {
			warnRelativeGitEnv("XDG_CONFIG_HOME", xdg)
		}
		if homeNamesOneDirectory {
			paths = append(paths, filepath.Join(home, ".config", "git"),
				filepath.Join(home, ".config", "git", "config"))
		}
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
// --includes because git turns include expansion OFF when a specific file is
// named, and --global names one: without it only the top level is reported, and
// an [include] inside an included file names a path wt would never hear about.
// Verified — a two-deep include is invisible without the flag and reported with
// it, its origin being the file that declared it, which is what a relative value
// there has to resolve against.
//
// Read from git rather than parsed here: the values are wanted before the files
// exist, and `git config --global` reports them either way. With --show-origin,
// so a relative value can be resolved the way git resolves it — against the
// directory of the config file that declared it, not against the process's
// working directory. `path = dotfiles/gitconfig` in ~/.gitconfig is ~/dotfiles,
// and is as much an armed slot as the absolute spelling of the same thing.
func gitGlobalIncludePaths() []string {
	// Both scopes, since --system carries the same include machinery and
	// GIT_CONFIG_SYSTEM can point it at a file the user can write.
	return append(gitIncludePathsIn("--global"), gitIncludePathsIn("--system")...)
}

func gitIncludePathsIn(scope string) []string {
	out, err := exec.Command("git", "config", scope, "--includes", "--show-origin", "--null",
		"--get-regexp", `^include(if\..*)?\.path$`).Output()
	if err != nil {
		return nil
	}
	// --show-origin --null emits the origin and the key/value as two separate
	// NUL-terminated fields, so they are read in pairs.
	fields := strings.Split(string(out), "\x00")
	var paths []string
	for i := 0; i+1 < len(fields); i += 2 {
		origin, ok := strings.CutPrefix(fields[i], "file:")
		if !ok {
			// blob:, command line:, standard input: — none of which name a
			// directory to resolve a relative include against.
			continue
		}
		_, value, hasValue := strings.Cut(fields[i+1], "\n")
		if !hasValue {
			continue
		}
		path := expandGitPath(value)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(origin), path)
		}
		paths = append(paths, path)
	}
	return paths
}

// homeEnvName names the variable os.UserHomeDir actually read, so a warning
// about it names something the user can go and change.
func homeEnvName() string {
	switch runtime.GOOS {
	case "windows":
		return "USERPROFILE"
	case "plan9":
		return "home"
	default:
		return "HOME"
	}
}

// relativeGitEnvWarnings keeps the notice to once per variable per process.
var relativeGitEnvWarnings sync.Map

// warnRelativeGitEnv says out loud that this PR's promise does not hold for this
// environment.
//
// git resolves a relative GIT_CONFIG_GLOBAL or XDG_CONFIG_HOME against the
// working directory, so such a value names a different file in every repository
// — including one a repository committed, holding core.hooksPath. wt cannot
// close that: the file is read the moment any git command runs there, whatever
// wt does or refuses to do, and nothing wt does created it. There is no
// placement to refuse. But "nothing runs until you approve it" is silently
// untrue on such a machine, and the only wrong answer is to keep quiet.
func warnRelativeGitEnv(name, value string) {
	if _, seen := relativeGitEnvWarnings.LoadOrStore(name, true); seen {
		return
	}
	// The true reason, not the common one: /proc/self/cwd/.gitconfig is an
	// absolute path, and telling someone who set that it is "relative" is a
	// warning they can see is wrong, which is a warning they learn to skip.
	why := "is a relative path"
	if filepath.IsAbs(value) {
		why = "names a different file depending on which process asks"
	}
	fmt.Fprintf(os.Stderr,
		"⚠ %s is set to %q, which %s.\n"+
			"  git resolves it per directory, so a repository that commits a file by that name\n"+
			"  supplies your global git config — including core.hooksPath, which wt cannot gate.\n"+
			"  Set it to a path that names one directory wherever wt runs.\n\n",
		name, value, why)
}

// gitHookDirIsWhatRuns explains a placement onto a git directory. Phrased to
// read after "which is", like the rest of the owned table.
const gitHookDirIsWhatRuns = "inside a git directory, where git keeps the hooks it runs for that " +
	"repository — a worktree there is one repository writing another's .git"

// bareGitDirIsWhatRuns explains a placement inside a repository whose name does
// not say it is one. Phrased to read after "which is", like the rest.
const bareGitDirIsWhatRuns = "inside a git directory, where git keeps the hooks it runs for that " +
	"repository — a bare repository is a git directory with no .git in its name, and its hooks " +
	"sit directly in it"

// gitDirAbove names the git directory an ancestor of path is, asking git rather
// than reading the name.
//
// pathInsideAGitDir looks for a ".git" component, which is every repository
// cloned the ordinary way and no bare one: `git init --bare /srv/git/project`
// keeps its hooks at /srv/git/project/hooks, and there is nothing in the name to
// see. Nor is ending in ".git" reliable — that is a convention, and the check is
// for a whole path component anyway.
//
// A hooks/ directory holding nothing is all `git worktree add` asks for, and one
// holding nothing is the normal case: a repository cloned with --template=, or
// one whose .sample files were removed, which is most of them on a server.
// Verified — worktree add checks out there and leaves an executable post-receive
// behind, which the next push to that repository runs.
//
// git is asked because git is the one that decides. For a directory that does
// not exist it says no, so the walk continues past a missing parent to the ones
// above it rather than stopping. Bounded, because a path deep enough to matter
// is a path something else has already gone wrong with.
func gitDirAbove(path string) (string, bool) {
	dir, prev := path, ""
	for range 64 {
		if dir == prev || dir == "" {
			break
		}
		out, err := exec.Command("git", "rev-parse", "--resolve-git-dir", dir).Output()
		if err == nil && gitOutputPath(out) != "" {
			return dir, true
		}
		dir, prev = filepath.Dir(dir), dir
	}
	return "", false
}

// pathInsideAGitDir reports whether a worktree at path would land inside some
// repository's .git.
//
// gitRepoOwnedPaths covers the repository wt is standing in, and stops there —
// but the mechanism does not care whose .git it is. A pattern naming
// ~/src/victim/.git/hooks reaches an empty directory on any clone made with no
// init template, `git worktree add` checks out into an empty directory happily,
// and the next checkout in *victim* runs what was left there. Nothing about that
// requires the attacker's repository to be the victim.
//
// By name rather than by asking git, because the question is about a path that
// does not exist yet and the repository it belongs to may not either. A ".git"
// component is not something a worktree path has for any other reason.
//
// The limit worth saying out loud: this finds the ordinary layout. A bare
// repository keeps its hooks at <repo>/hooks with no .git anywhere in the path,
// and wt cannot enumerate every repository on the machine to find those.
//
// Folded, because the guard refuses: on a case-insensitive volume
// ~/src/victim/.GIT/hooks IS ~/src/victim/.git/hooks, and a pattern is free to
// spell it either way. foldPath drops Win32's trailing dots and spaces too, so
// ".git." does not walk past this on Windows.
func pathInsideAGitDir(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(foldPath(path)), "/") {
		if part == ".git" {
			return true
		}
	}
	return false
}

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
	// GIT_TEMPLATE_DIR before the config keys, since it is the spelling that
	// needs no config file at all.
	if p := expandGitPath(os.Getenv("GIT_TEMPLATE_DIR")); filepath.IsAbs(p) {
		paths = append(paths, p)
	}
	for _, key := range gitExecutablePathKeys {
		out, err := exec.Command("git", "config", "--get", key).Output()
		if err != nil {
			continue
		}
		if p := expandGitPath(gitOutputPath(out)); filepath.IsAbs(p) {
			paths = append(paths, p)
		} else if p != "" {
			// A relative value is not confined to the repository: git resolves
			// core.hooksPath against the top of the working tree, and "../shared-
			// hooks" leaves it from there. Resolved and guarded rather than
			// dropped — the sibling it names is exactly the sort of path nothing
			// has created yet, which is what a pattern can reach.
			//
			// core.fsmonitor's "true" and "false" resolve to directories inside
			// the working tree, which a worktree cannot be placed in anyway.
			if top, err := gitToplevel(); err == nil && top != "" {
				paths = append(paths, filepath.Join(top, p))
			}
		}
	}
	return paths
}

// gitExecutablePathKeys are the config settings that name a place git will run
// something from, rather than a setting git merely reads.
//
// Each is the same shape as core.hooksPath: a path git consults, which git does
// not mind being absent, so a value naming a directory that is not there yet is
// an armed slot a pattern can fill.
//
//   - core.hooksPath — where git looks for hooks instead of $GIT_DIR/hooks.
//   - init.templateDir — whose hooks/ is COPIED into every repository git
//     creates afterwards, so filling it arms every future clone rather than one
//     repository. GIT_TEMPLATE_DIR is the same thing from the environment.
//   - core.fsmonitor — a hook program git runs on any command that reads the
//     index, which is nearly all of them.
//
// Not a claim to be exhaustive about every git setting naming a program: most
// name an installed binary (core.pager, gpg.program) rather than a directory
// waiting to be created, and chasing them one key at a time has a floor. The
// bounded fix is anchoring a repository-supplied absolute pattern inside the
// user's own tree, which is issue #154.
var gitExecutablePathKeys = []string{"core.hooksPath", "init.templateDir", "core.fsmonitor"}

// expandGitPath expands a leading "~" the way git does, which includes the
// "~user" form expandTilde deliberately leaves alone.
//
// git runs core.hooksPath = ~alice/armed/hooks through getpwnam and arrives at
// an absolute path; wt reading the same value as relative would skip it, and
// skipping is not guarding. Only ever widens what the guard refuses, so a lookup
// that fails leaves the value naming nothing rather than naming itself.
//
// "/" only as the separator, because "~user" is a Unix idea — git needs a passwd
// entry to expand it, and Windows has none.
func expandGitPath(value string) string {
	if p := expandTilde(value); p != value {
		return p
	}
	if !strings.HasPrefix(value, "~") {
		return value
	}
	name, rest, _ := strings.Cut(value[1:], "/")
	if name == "" {
		return value
	}
	u, err := user.Lookup(name)
	if err != nil || !filepath.IsAbs(u.HomeDir) {
		return ""
	}
	return filepath.Join(u.HomeDir, rest)
}

func wtStateAtPath(path string) string {
	owned := []struct{ path, what string }{
		{configDir(), "where wt keeps its config file and its record of approved hooks"},
		{configFilePath, "your config file"},
		// Named as well as its directory, because the two need not be in the
		// same place: trust.toml is often a symlink out of a dotfiles
		// repository, and guarding ~/.config/wt says nothing about where such a
		// link points. A dangling one — dotfiles not cloned yet — is a path a
		// pattern can render onto, and what gets checked out there IS the record
		// of what you have approved.
		{trustFilePath(), "where wt keeps its record of approved hooks"},
	}
	for _, p := range gitGlobalConfigPaths() {
		owned = append(owned, struct{ path, what string }{p, gitConfigIsWhatRuns})
	}
	for _, p := range gitRepoOwnedPaths() {
		owned = append(owned, struct{ path, what string }{p, gitHookDirIsWhatRuns})
	}
	// Asked of the path as written and again once resolved: the pattern is what
	// names a .git, but a symlink is what hides that it does.
	if pathInsideAGitDir(path) {
		return gitHookDirIsWhatRuns
	}
	path, ok := canonicalExistingPath(path)
	if !ok {
		return unfollowableChain
	}
	if pathInsideAGitDir(path) {
		return gitHookDirIsWhatRuns
	}
	// And then git's own answer, for the repositories the name test cannot see.
	if dir, ok := gitDirAbove(path); ok {
		return fmt.Sprintf("%s (%s)", bareGitDirIsWhatRuns, dir)
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

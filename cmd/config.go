package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config represents the wt configuration file structure.
type Config struct {
	Root        string `toml:"root"`
	RepoRoot    string `toml:"repo_root"`
	Strategy    string `toml:"strategy"`
	Pattern     string `toml:"pattern"`
	Separator   string `toml:"separator"`
	Hooks       Hooks  `toml:"hooks"`
	HooksPolicy string `toml:"hooks_policy"`
	RepoPattern string `toml:"repo_pattern"`
	Files       Files  `toml:"files"`

	// Context holds path-matched rules supplying environment variables to
	// pattern rendering. Decoded from the user's config file only — see
	// contextRules in context.go.
	Context []ContextRule `toml:"context"`
}

// Files controls materialisation of untracked/ignored files into new worktrees.
//
// Unlike every other setting, the three list keys accumulate across config
// layers instead of replacing: a user who wants .env everywhere plus whatever
// a project adds has no way to express that if the layers overwrite each
// other. See accumulateFilePatterns.
type Files struct {
	Copy        []string `toml:"copy"`
	Link        []string `toml:"link"`
	Exclude     []string `toml:"exclude"`
	CopyIgnored bool     `toml:"copy_ignored"`
}

// Hooks holds pre/post command hook commands.
type Hooks struct {
	PreCreate    []string `toml:"pre_create"`
	PostCreate   []string `toml:"post_create"`
	PreCheckout  []string `toml:"pre_checkout"`
	PostCheckout []string `toml:"post_checkout"`
	PreRemove    []string `toml:"pre_remove"`
	PostRemove   []string `toml:"post_remove"`
	PrePR        []string `toml:"pre_pr"`
	PostPR       []string `toml:"post_pr"`
	PreMR        []string `toml:"pre_mr"`
	PostMR       []string `toml:"post_mr"`
	PreClone     []string `toml:"pre_clone"`
	PostClone    []string `toml:"post_clone"`
}

// configSource tracks where each config value came from.
type configSource struct {
	Root        string
	RepoRoot    string
	Strategy    string
	Pattern     string
	Separator   string
	RepoPattern string
	HooksPolicy string
	CopyIgnored string
}

// configFilePath is the resolved path to the config file (set during loading).
var configFilePath string

// configFileFound indicates whether the config file was found during loading.
var configFileFound bool

// configRepoPath is the resolved path to the repo-level .wt.toml (set during loading).
var configRepoPath string

// configRepoFound indicates whether a repo-level .wt.toml was found during loading.
var configRepoFound bool

// configRepoKey is the repository identity a trust record is pinned to,
// resolved while the .wt.toml is being read.
//
// Resolved here rather than when a hook is about to run because by then the
// working directory may be gone: `wt remove` fires post_remove hooks after git
// has deleted the worktree the user was standing in, and `git rev-parse` from a
// deleted directory answers nothing. Config load is the last moment the answer
// is reliably available.
var configRepoKey string

// configRepoSHA is the sha256 of the repo-level .wt.toml bytes that were
// actually decoded, and is what hook approvals are pinned to.
var configRepoSHA string

// configSources tracks the origin of each resolved value.
var configSources configSource

// worktreeHooks holds the effective (merged) hook configuration.
var worktreeHooks Hooks

// repoConfigHooks holds the hooks the repo-level .wt.toml supplied, kept
// separate so they can be shown for approval before anything runs.
var repoConfigHooks Hooks

// hookSources records which config layer supplied each hook event's commands.
// The merge below replaces a whole event at a time, so one source per event is
// exact — and it is what tells runHooks whether it is looking at commands the
// user wrote or commands a repository shipped.
var hookSources = map[string]string{}

// hooksPolicy is the configured hook approval policy (see hookPolicy* in
// hooks.go). Deliberately loaded from the user's config file only.
var hooksPolicy string

// The accumulated [files] pattern lists, in layer order, each pattern tagged
// with the layer that supplied it. See accumulateFilePatterns for why these
// accumulate rather than replace, and resolveFileConfig for the
// .worktreeinclude layer that is added on top of them at use time.
var (
	filesCopy    []layeredPattern
	filesLink    []layeredPattern
	filesExclude []layeredPattern
)

// filesCopyIgnored is the resolved copy_ignored setting. It is a scalar, so
// unlike the lists it follows the ordinary precedence chain.
var filesCopyIgnored bool

// layeredPattern is one [files] pattern together with the config layer it came
// from, so `wt info` can explain why a pattern is in effect.
type layeredPattern struct {
	Pattern string `json:"pattern"`
	Source  string `json:"source"`
}

// Clone placement configuration, loaded by loadWorktreeConfig.
var (
	// reposRoot is the base directory for canonical clones (wt clone).
	reposRoot string
	// repoPattern is the placement layout for canonical clones.
	repoPattern string
)

// configFlag is the --config flag value (set by cobra).
var configFlag string

// gitRepoRootFn returns the git repository root directory.
// It is a variable so tests can inject a fake implementation.
var gitRepoRootFn = defaultGitRepoRoot

// defaultGitRepoRoot uses git rev-parse to find the repo root.
func defaultGitRepoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// gitConfigScope identifies which git config file a value came from.
type gitConfigScope string

const (
	gitScopeGlobal gitConfigScope = "global"
	gitScopeLocal  gitConfigScope = "local"
)

// gitConfigFn reads the wt.* keys from a single git config scope.
// It is a variable so tests can inject a fake implementation.
var gitConfigFn = defaultGitConfig

// gitConfigBoolKeys names the wt.* keys read as git booleans. They are the only
// ones for which a valueless key is meaningful: `[wt]\n\tcopyIgnored` with no
// "=" is how git spells true.
var gitConfigBoolKeys = map[string]bool{
	"wt.copyignored": true,
}

// defaultGitConfig reads wt.* keys from the given git config scope.
//
// Only the last value of each key is kept: git config returns multi-valued keys
// in lowest-to-highest precedence order, and for scalar settings the final one
// is the effective value.
//
// A missing scope is not an error. `git config --get-regexp` exits 1 when no key
// matches, and exits with other non-zero codes when the scope is unavailable
// (for example --local outside a repository). Both mean "nothing configured
// here", so any error yields an empty map rather than failing the command. This
// matches how a malformed config file is treated on the TOML path, which is
// also skipped rather than reported.
func defaultGitConfig(scope gitConfigScope) map[string]string {
	// --null makes the output unambiguous: records are NUL-separated and the
	// key is separated from its value by a newline, so values containing
	// spaces or newlines survive intact. A key set with no value at all has no
	// newline in its record, which is how it is told apart from an empty one.
	cmd := exec.Command("git", "config", "--"+string(scope), "--null", "--get-regexp", `^wt\.`)
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	values := make(map[string]string)
	for _, record := range strings.Split(string(output), "\x00") {
		if record == "" {
			continue
		}
		key, value, hasValue := strings.Cut(record, "\n")
		// git lowercases the section and the variable name, but preserves the
		// case of any subsection. Only the fully lowercase keys are read, so
		// normalising here keeps lookups simple.
		key = strings.ToLower(key)
		if !hasValue && !gitConfigBoolKeys[key] {
			// `[wt]\n\tseparator` with no "=": git reports it as valueless.
			// For a string setting that means nothing, so it is treated as
			// unset rather than as an empty string.
			continue
		}
		// A valueless boolean is git's spelling of true, and parseGitBool
		// reads the empty value that way.
		values[key] = value
	}
	return values
}

// applyGitConfig applies wt.* scalar settings from one git config scope.
// Hooks are intentionally not read from git config: representing lists is
// possible via multi-valued keys, but the merge semantics (replace vs append,
// and how to clear inherited hooks) are not settled for the TOML sources
// either, so git config stays scalar-only.
func applyGitConfig(scope gitConfigScope, sourceLabel string) {
	values := gitConfigFn(scope)
	if len(values) == 0 {
		return
	}

	if v := strings.TrimSpace(values["wt.root"]); v != "" {
		worktreeRoot = expandHome(v)
		configSources.Root = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.repo_root"]); v != "" {
		reposRoot = expandHome(v)
		configSources.RepoRoot = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.strategy"]); v != "" {
		worktreeStrategy = strings.ToLower(v)
		configSources.Strategy = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.pattern"]); v != "" {
		worktreePattern = v
		configSources.Pattern = sourceLabel
	}
	// separator is applied even when empty: an empty separator is a meaningful
	// value, matching how WORKTREE_SEPARATOR is handled.
	if v, ok := values["wt.separator"]; ok {
		worktreeSeparator = v
		configSources.Separator = sourceLabel
	}
	if v := strings.TrimSpace(values["wt.repo_pattern"]); v != "" {
		repoPattern = v
		configSources.RepoPattern = sourceLabel
	}
	// copy_ignored is the one [files] key readable from git config: it is a
	// scalar. The list keys would need --get-all handling for multi-valued
	// keys, and there is no accumulation story for git config layers, so they
	// stay TOML-only (documented in docs/configuration.md).
	//
	// It is spelled wt.copyIgnored there, not wt.copy_ignored: git config
	// variable names allow only alphanumerics and "-", and it rejects an
	// underscore outright ("error: invalid key"), a whole config file holding
	// one included. git lowercases the name it reports, hence the lookup key.
	if v, ok := values["wt.copyignored"]; ok {
		if b, ok := parseGitBool(v); ok {
			filesCopyIgnored = b
			configSources.CopyIgnored = sourceLabel
		}
	}
}

// parseGitBool interprets a git-config boolean. git accepts several spellings
// for each value, and a key present with no value means true.
func parseGitBool(v string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1", "":
		return true, true
	case "false", "no", "off", "0":
		return false, true
	default:
		return false, false
	}
}

// accumulateFilePatterns appends patterns to an accumulated list, tagging each
// with the layer that supplied it and dropping duplicates.
//
// Accumulating rather than replacing is deliberate and is the only sane
// behaviour here: a user whose own config says "always copy .env" and who then
// works in a repo whose .wt.toml adds "copy config/local.yml" wants both, and
// with replace semantics the repo would silently drop the .env. Excludes
// accumulate for the mirror-image reason — a global "never copy *.pem" has to
// hold against any repository's config.
//
// First-seen order is preserved so the effective list reads as
// config file, then repo config, then .worktreeinclude.
func accumulateFilePatterns(dst []layeredPattern, patterns []string, source string) []layeredPattern {
	for _, p := range patterns {
		// Patterns are kept verbatim: gitignore gives a trailing space meaning
		// (it is stripped unless escaped, as in "file\ "), so trimming here
		// would turn "file\ " into "file\" before the ignore parser ever saw
		// it. Blank-only entries are the one thing worth dropping.
		if strings.TrimSpace(p) == "" {
			continue
		}
		duplicate := false
		for _, existing := range dst {
			if existing.Pattern == p {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		dst = append(dst, layeredPattern{Pattern: p, Source: source})
	}
	return dst
}

// defaultConfigTemplate is the content written by `wt config init`.
const defaultConfigTemplate = `# wt configuration file
# See: https://github.com/timvw/wt#configuration

# Root directory for worktrees (default: ~/dev/worktrees)
# root = "~/dev/worktrees"

# Base directory for canonical clones created by 'wt clone' (default: ~/dev/repos).
# repo_root = "~/dev/repos"

# Worktree placement strategy
# Options: global, sibling-repo, parent-branches, parent-worktrees,
#          parent-dotdir, inside-dotdir, custom
# strategy = "global"

# Custom pattern (used when strategy = "custom", or to override any strategy's default)
# Available variables: {.worktreeRoot}, {.repo.Name}, {.repo.Main},
#                      {.repo.Owner}, {.repo.Host}, {.branch},
#                      {.env.VARNAME} (access environment variables, e.g. {.env.USER})
# pattern = "{.worktreeRoot}/{.repo.Name}/{.branch}"

# Separator replaces "/" and "\" in template value variables ({.branch}, {.repo.Owner}, {.env.*})
# Default is "/" (no transformation — slashes create subdirectories).
# Set to "-" or "_" for flat paths (e.g. feat/foo -> feat-foo).
# Does NOT affect path variables ({.repo.Main}, {.worktreeRoot}).
# separator = "/"

# Example: group worktrees by a FEATURE environment variable
# strategy = "custom"
# pattern = "{.worktreeRoot}/{.env.FEATURE}/{.repo.Name}"

# Placement layout for repos cloned by 'wt clone'. Variables: {.repoRoot},
# {.repo.Host}, {.repo.Owner}, {.repo.Name}, {.branch}, {.env.VARNAME}.
# {.branch} is the remote's default branch (via git ls-remote, fallback "main"),
# which makes the clone a valid main-worktree slot for sibling strategies.
# repo_pattern = "{.repoRoot}/{.repo.Host}/{.repo.Owner}/{.repo.Name}/{.branch}"

# Example: add your own grouping level (a "category", a client, a year) with an
# environment variable, then override it per invocation:
#   WT_CATEGORY=work wt clone o/r
# Give it a default in your shell rc: referencing an unset variable is an error,
# while an empty value just collapses the segment away.
# repo_pattern = "{.repoRoot}/{.env.WT_CATEGORY}/{.repo.Owner}/{.repo.Name}/{.branch}"

# Set variables for a whole tree of repositories, instead of exporting them per
# command. Each rule matches a path prefix; every matching rule applies, and
# later rules override earlier ones per variable. An exported variable always
# wins over a rule.
#
# Matched against the repository's main checkout for worktree commands, and
# against the current directory for 'wt clone' (no repo exists yet).
#
# [[context]]
# when_path = "~/dev/repos/work"
# env = { WT_CATEGORY = "work" }
#
# [[context]]
# when_path = "~/dev/repos/personal"
# env = { WT_CATEGORY = "personal" }

# Files — materialise untracked files into every new worktree
# Runs on create/checkout/pr/mr, after 'git worktree add' and before post_* hooks.
# Patterns use gitignore syntax and are relative to the main worktree root.
# Only untracked, git-ignored files are candidates: tracked files are already in
# the new worktree via the checkout and are never touched.
# Copies use a reflink on APFS/Btrfs/XFS, so size is not a reason to hesitate.
# An existing destination file is skipped, never overwritten (use 'wt copy --force').
# Skip once with --no-copy; switch the feature off with WT_FILES_DISABLED=1.
#
# The three list keys accumulate with a repo's .wt.toml and its .worktreeinclude
# rather than being replaced by them; 'exclude' is applied last and always wins.
#
# [files]
# copy = [".env", ".envrc", ".claude/settings.local.json"]
# link = ["node_modules", ".venv"]
# exclude = ["*.pem", "*.key"]
# copy_ignored = false

# Hooks — run commands before/after wt operations
# Available env vars in hooks: $WT_PATH, $WT_BRANCH, $WT_MAIN,
#                              $WT_REPO_NAME, $WT_REPO_HOST, $WT_REPO_OWNER
# Pre-hooks abort on failure; post-hooks warn only.
# Set WT_HOOKS_DISABLED=1 to skip all hooks.
#
# Hooks from a repository's committed .wt.toml are not run until you approve
# them ('wt trust'). Hooks from THIS file are yours, so they run as-is unless
# you ask for more:
#   prompt-untrusted  (default) approve hooks that came from a repo's .wt.toml
#   prompt-all        confirm every hook batch, whatever supplied it
#   trusted-only      never prompt; skip anything not already approved
#   off               run no hooks at all
# hooks_policy = "prompt-untrusted"
# NOTE: Always quote path variables ("$WT_PATH") to handle spaces in paths.
#
# [hooks]
# post_create = ["cd \"$WT_PATH\" && direnv allow"]
# post_checkout = ["cd \"$WT_PATH\" && npm install"]
# pre_remove = ["echo \"Removing $WT_PATH\""]
# post_clone = ["cd \"$WT_PATH\" && git status"]
`

// configDir returns the directory where wt config files are stored.
func configDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "wt")
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "wt")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "wt")
}

// resolveConfigPath determines which config file to use.
// Priority: --config flag > WT_CONFIG env var > default location.
func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if envPath := os.Getenv("WT_CONFIG"); envPath != "" {
		return envPath
	}
	return filepath.Join(configDir(), "config.toml")
}

// loadWorktreeConfig loads configuration from git config, files and environment
// variables.
//
// Precedence, highest first:
//
//	env vars > local git config > repo .wt.toml > config file > global git
//	config > defaults
//
// --config and WT_CONFIG are not a precedence layer of their own: they select
// which TOML file is loaded, and its values still sit below .wt.toml and env.
func loadWorktreeConfig() {
	// 1. Start with defaults
	home, _ := os.UserHomeDir()
	defaultRoot := filepath.Join(home, "dev", "worktrees")

	worktreeRoot = defaultRoot
	reposRoot = filepath.Join(home, "dev", "repos")
	worktreeStrategy = "global"
	worktreePattern = ""
	worktreeSeparator = "/"

	configSources = configSource{
		Root:        "default",
		RepoRoot:    "default",
		Strategy:    "default",
		Pattern:     "default",
		Separator:   "default",
		RepoPattern: "default",
		HooksPolicy: "default",
		CopyIgnored: "default",
	}

	// Reset hooks
	worktreeHooks = Hooks{}
	repoConfigHooks = Hooks{}
	hookSources = map[string]string{}
	hooksPolicy = ""
	contextRules = nil

	// Reset [files]
	filesCopy = nil
	filesLink = nil
	filesExclude = nil
	filesCopyIgnored = false

	repoPattern = defaultRepoPattern

	// 2. Global git config (~/.gitconfig) — the broadest fallback, below wt's
	// own config file.
	applyGitConfig(gitScopeGlobal, "git config (global)")

	// 3. Load config file
	configFilePath = resolveConfigPath(configFlag)
	configFileFound = false

	if _, err := os.Stat(configFilePath); err == nil {
		configFileFound = true
		var cfg Config
		if md, err := toml.DecodeFile(configFilePath, &cfg); err == nil {
			if cfg.Root != "" {
				worktreeRoot = expandHome(cfg.Root)
				configSources.Root = "config file"
			}
			if cfg.RepoRoot != "" {
				reposRoot = expandHome(cfg.RepoRoot)
				configSources.RepoRoot = "config file"
			}
			if cfg.Strategy != "" {
				worktreeStrategy = strings.ToLower(strings.TrimSpace(cfg.Strategy))
				configSources.Strategy = "config file"
			}
			if cfg.Pattern != "" {
				worktreePattern = strings.TrimSpace(cfg.Pattern)
				configSources.Pattern = "config file"
			}
			if cfg.Separator != "" {
				worktreeSeparator = cfg.Separator
				configSources.Separator = "config file"
			}
			if cfg.RepoPattern != "" {
				repoPattern = strings.TrimSpace(cfg.RepoPattern)
				configSources.RepoPattern = "config file"
			}
			worktreeHooks = cfg.Hooks
			for _, event := range hookEvents {
				if len(hooksOf(cfg.Hooks, event)) > 0 {
					hookSources[event] = hookSourceConfigFile
				}
			}
			if cfg.HooksPolicy != "" {
				hooksPolicy = strings.ToLower(strings.TrimSpace(cfg.HooksPolicy))
				configSources.HooksPolicy = "config file"
			}
			contextRules = cfg.Context

			filesCopy = accumulateFilePatterns(filesCopy, cfg.Files.Copy, "config file")
			filesLink = accumulateFilePatterns(filesLink, cfg.Files.Link, "config file")
			filesExclude = accumulateFilePatterns(filesExclude, cfg.Files.Exclude, "config file")
			// IsDefined rather than a non-zero check: copy_ignored is a bool,
			// so "written as false" and "not written" are the same value and
			// only the metadata can tell them apart.
			if md.IsDefined("files", "copy_ignored") {
				filesCopyIgnored = cfg.Files.CopyIgnored
				configSources.CopyIgnored = "config file"
			}
		}
	}

	// 4. Load repo-level .wt.toml (overrides global config, but NOT root and
	//    NOT the clone settings). `wt clone` acquires a repository unrelated to
	//    whichever one you happen to be standing in, so letting that repo's
	//    .wt.toml redirect the destination or run clone hooks would be wrong.
	configRepoPath = ""
	configRepoFound = false
	configRepoSHA = ""
	configRepoKey = ""

	if repoRoot, err := gitRepoRootFn(); err == nil {
		repoConfigPath := filepath.Join(repoRoot, ".wt.toml")
		configRepoPath = repoConfigPath
		// Read once and hash those exact bytes, rather than hashing the path
		// again when the hooks are about to run. Approval has to be pinned to
		// the commands actually decoded here: re-reading later would leave a
		// window in which the file is swapped, and wt would check one file's
		// hash while running another file's commands.
		if data, err := os.ReadFile(repoConfigPath); err == nil {
			configRepoFound = true
			configRepoSHA = hashBytes(data)
			if key, err := repoTrustKeyFn(); err == nil {
				configRepoKey = key
			}
			var repoCfg Config
			if md, err := toml.Decode(string(data), &repoCfg); err == nil {
				// root, repo_root and repo_pattern are intentionally NOT loaded
				// from repo config
				if repoCfg.Strategy != "" {
					worktreeStrategy = strings.ToLower(strings.TrimSpace(repoCfg.Strategy))
					configSources.Strategy = "repo config"
				}
				if repoCfg.Pattern != "" {
					worktreePattern = strings.TrimSpace(repoCfg.Pattern)
					configSources.Pattern = "repo config"
				}
				if repoCfg.Separator != "" {
					worktreeSeparator = repoCfg.Separator
					configSources.Separator = "repo config"
				}
				// hooks_policy is deliberately NOT read from repo config: a
				// repository choosing how closely wt scrutinises that same
				// repository's hooks would defeat the point.

				// Merge hooks: repo hooks override per-hook type, unset hooks keep
				// global values. Which layer won is recorded in hookSources, because
				// commands that arrived here from a committed file need the user's
				// approval before they run (see approveHooks).
				repoConfigHooks = repoCfg.Hooks
				// pre_clone/post_clone are deliberately not merged from repo
				// config: clone targets a different repository than this one.
				repoConfigHooks.PreClone = nil
				repoConfigHooks.PostClone = nil
				for _, event := range hookEvents {
					cmds := hooksOf(repoConfigHooks, event)
					if len(cmds) == 0 {
						continue
					}
					setHooks(&worktreeHooks, event, cmds)
					hookSources[event] = hookSourceRepoConfig
				}

				// [files] needs no trust gate. Hooks are arbitrary command
				// execution, which is why they have one; [files] is
				// declarative data whose only power is to move bytes from the
				// main worktree into the new worktree — see the F1-F7
				// invariants in files.go. Gating it would also defeat its main
				// purpose, which is exactly the team-config case: a repo
				// declaring "everyone needs .env here".
				filesCopy = accumulateFilePatterns(filesCopy, repoCfg.Files.Copy, "repo config")
				filesLink = accumulateFilePatterns(filesLink, repoCfg.Files.Link, "repo config")
				filesExclude = accumulateFilePatterns(filesExclude, repoCfg.Files.Exclude, "repo config")
				if md.IsDefined("files", "copy_ignored") {
					filesCopyIgnored = repoCfg.Files.CopyIgnored
					configSources.CopyIgnored = "repo config"
				}
			}
		}
	}

	// 5. Local git config (.git/config) — personal, machine-local settings that
	// are not committed, so they outrank the committed .wt.toml. Unlike
	// .wt.toml, this may set root: it is user-controlled local state rather
	// than project policy arriving via a pull request.
	//
	// Linked worktrees share the main repository's .git/config, so a value set
	// once in the main checkout applies from every worktree of that repo.
	applyGitConfig(gitScopeLocal, "git config (local)")

	// 6. Environment variables override every file-based source
	if v := os.Getenv("WORKTREE_ROOT"); v != "" {
		worktreeRoot = v
		configSources.Root = "env: WORKTREE_ROOT"
	}
	if v := os.Getenv("WT_REPO_ROOT"); v != "" {
		reposRoot = expandHome(v)
		configSources.RepoRoot = "env: WT_REPO_ROOT"
	}
	if v := os.Getenv("WT_REPO_PATTERN"); v != "" {
		repoPattern = strings.TrimSpace(v)
		configSources.RepoPattern = "env: WT_REPO_PATTERN"
	}
	if v := os.Getenv("WORKTREE_STRATEGY"); v != "" {
		worktreeStrategy = strings.ToLower(strings.TrimSpace(v))
		configSources.Strategy = "env: WORKTREE_STRATEGY"
	}
	if v := os.Getenv("WORKTREE_PATTERN"); v != "" {
		worktreePattern = strings.TrimSpace(v)
		configSources.Pattern = "env: WORKTREE_PATTERN"
	}
	if v, ok := os.LookupEnv("WORKTREE_SEPARATOR"); ok {
		worktreeSeparator = v
		configSources.Separator = "env: WORKTREE_SEPARATOR"
	}
	if v, ok := os.LookupEnv("WT_COPY_IGNORED"); ok {
		if b, ok := parseGitBool(v); ok {
			filesCopyIgnored = b
			configSources.CopyIgnored = "env: WT_COPY_IGNORED"
		}
	}
}

// expandHome replaces a leading ~ with the user's home directory
// and expands environment variables ($VAR, ${VAR}, and %VAR% on Windows).
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		path = filepath.Join(home, path[1:])
	}
	expanded := os.ExpandEnv(path)
	if runtime.GOOS == "windows" {
		expanded = expandWindowsEnv(expanded)
	}
	return expanded
}

// expandWindowsEnv expands %VAR% style environment variables.
func expandWindowsEnv(path string) string {
	for {
		start := strings.Index(path, "%")
		if start == -1 {
			break
		}
		end := strings.Index(path[start+1:], "%")
		if end == -1 {
			break
		}
		end += start + 1
		varName := path[start+1 : end]
		if val, ok := os.LookupEnv(varName); ok {
			path = path[:start] + val + path[end+1:]
		} else {
			path = path[:start] + path[end+1:]
		}
	}
	return path
}

// writeDefaultConfig creates a default config file at the given path.
func writeDefaultConfig(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config file already exists: %s (use --force to overwrite)", path)
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(defaultConfigTemplate), 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

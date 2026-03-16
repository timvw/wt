package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, run func()) string {
	t.Helper()

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create stdout pipe: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = origStdout
	}()

	run()

	if err := w.Close(); err != nil {
		t.Fatalf("failed to close write pipe: %v", err)
	}

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("failed to read captured output: %v", err)
	}

	return buf.String()
}

func topicHasTextAndJSONExamples(topic exampleTopic) bool {
	hasText := false
	hasJSON := false
	for _, ex := range topic.Examples {
		if ex.TextExample != "" {
			hasText = true
		}
		if ex.JSONExample != "" {
			hasJSON = true
		}
	}
	return hasText && hasJSON
}

func TestExamplesRejectsTopicArgument(t *testing.T) {
	err := examplesCmd.Args(examplesCmd, []string{"create"})
	if err == nil {
		t.Fatal("expected examples command to reject topic arguments")
	}
}

func TestSortedTopicsIncludesCreate(t *testing.T) {
	topics := sortedTopics()
	found := false
	for _, topic := range topics {
		if topic == "create" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected create topic in sortedTopics")
	}
}

func TestSortedTopicsIncludesMigrate(t *testing.T) {
	topics := sortedTopics()
	found := false
	for _, topic := range topics {
		if topic == "migrate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected migrate topic in sortedTopics")
	}
}

func TestExamplesCoverAllTopLevelCommands(t *testing.T) {
	expectedTopics := []string{
		"checkout",
		"create",
		"pr",
		"mr",
		"list",
		"remove",
		"cleanup",
		"migrate",
		"prune",
		"shellenv",
		"init",
		"info",
		"config",
		"examples",
		"version",
	}

	for _, topicName := range expectedTopics {
		if _, ok := exampleCatalog[topicName]; !ok {
			t.Fatalf("expected examples catalog to include topic %q", topicName)
		}
	}
}

func TestCreateExamplesIncludeOutcomeAndFailureModes(t *testing.T) {
	topic, ok := exampleCatalog["create"]
	if !ok {
		t.Fatal("expected create topic in catalog")
	}
	if len(topic.Examples) == 0 {
		t.Fatal("expected create topic to contain examples")
	}
	if topic.Examples[0].Outcome == "" {
		t.Fatal("expected create example to define an outcome")
	}
	if len(topic.Examples[0].FailureModes) == 0 {
		t.Fatal("expected create example to define failure modes")
	}
}

func TestRemoveExampleIncludesPathExample(t *testing.T) {
	topic, ok := exampleCatalog["remove"]
	if !ok {
		t.Fatal("expected remove topic in catalog")
	}
	if len(topic.Examples) == 0 {
		t.Fatal("expected remove topic to contain examples")
	}
	if topic.Examples[0].PathExample == "" {
		t.Fatal("expected remove example to define a path example")
	}
	if topic.Examples[0].PathBasis == "" {
		t.Fatal("expected remove example to define path basis")
	}
}

func TestListExampleIncludesRawTextSample(t *testing.T) {
	topic, ok := exampleCatalog["list"]
	if !ok {
		t.Fatal("expected list topic in catalog")
	}
	if len(topic.Examples) == 0 {
		t.Fatal("expected list topic to contain examples")
	}
	if topic.Examples[0].TextExample == "" {
		t.Fatal("expected list text example to define a raw text sample")
	}
}

func TestRemoveJSONExampleIncludesSamplePayload(t *testing.T) {
	topic, ok := exampleCatalog["remove"]
	if !ok {
		t.Fatal("expected remove topic in catalog")
	}
	if len(topic.Examples) < 2 {
		t.Fatal("expected remove topic to contain json example")
	}
	if topic.Examples[1].JSONExample == "" {
		t.Fatal("expected remove json example to define a sample payload")
	}
}

func TestAllExamplesHaveConcreteSamples(t *testing.T) {
	for topicName, topic := range exampleCatalog {
		for _, ex := range topic.Examples {
			if strings.Contains(ex.Command, "--format json") {
				if ex.JSONExample == "" {
					t.Fatalf("expected json example for topic %s command %q", topicName, ex.Command)
				}
				continue
			}

			if ex.TextExample == "" {
				t.Fatalf("expected text example for topic %s command %q", topicName, ex.Command)
			}
		}
	}
}

func TestListTextExampleLooksRawStyle(t *testing.T) {
	topic := exampleCatalog["list"]
	if len(topic.Examples) == 0 {
		t.Fatal("expected list examples")
	}
	if !strings.Contains(topic.Examples[0].TextExample, "[main]") {
		t.Fatal("expected raw-style branch marker in list text example")
	}
}

func TestCreateRemoveMigrateExamplesHaveStrategyVariantsInPathExamples(t *testing.T) {
	for _, topicName := range []string{"create", "remove", "migrate"} {
		topic, ok := exampleCatalog[topicName]
		if !ok {
			t.Fatalf("expected %s topic in catalog", topicName)
		}

		foundVariantExample := false
		for _, ex := range topic.Examples {
			if strings.Contains(ex.PathExample, "global") &&
				strings.Contains(ex.PathExample, "sibling-repo") &&
				strings.Contains(ex.PathExample, "parent-branches") &&
				strings.Contains(ex.PathExample, "parent-worktrees") &&
				strings.Contains(ex.PathExample, "custom pattern") {
				foundVariantExample = true
				break
			}
		}

		if !foundVariantExample {
			t.Fatalf("expected %s examples to include global, sibling-repo, parent-branches, parent-worktrees, and custom pattern variants", topicName)
		}
	}
}

func TestCreateRemoveMigrateStrategyExamplesAppearInTextAndJSONSamples(t *testing.T) {
	tests := []struct {
		topicName   string
		textNeedle  string
		jsonNeedle  string
		pathNeedle  string
		basisNeedle string
	}{
		{topicName: "create", textNeedle: "Path outcomes by strategy (static):", jsonNeedle: "\"path_outcomes_by_strategy\"", pathNeedle: "parent-branches", basisNeedle: "Static placeholders"},
		{topicName: "remove", textNeedle: "Path outcomes by strategy (static):", jsonNeedle: "\"path_outcomes_by_strategy\"", pathNeedle: "parent-branches", basisNeedle: "Static placeholders"},
		{topicName: "migrate", textNeedle: "Path outcomes by strategy switch (static):", jsonNeedle: "\"results\"", pathNeedle: "global -> parent-branches", basisNeedle: "<repo-main-parent>"},
	}

	for _, tc := range tests {
		topic, ok := exampleCatalog[tc.topicName]
		if !ok {
			t.Fatalf("expected %s topic in catalog", tc.topicName)
		}

		foundText := false
		foundJSON := false
		foundPath := false
		foundBasis := false

		for _, ex := range topic.Examples {
			if strings.Contains(ex.TextExample, tc.textNeedle) {
				foundText = true
			}
			if strings.Contains(ex.JSONExample, tc.jsonNeedle) {
				foundJSON = true
			}
			if strings.Contains(ex.PathExample, tc.pathNeedle) {
				foundPath = true
			}
			if strings.Contains(ex.PathBasis, tc.basisNeedle) {
				foundBasis = true
			}
		}

		if !foundText {
			t.Fatalf("expected %s examples to include strategy-influence text sample", tc.topicName)
		}
		if !foundJSON {
			t.Fatalf("expected %s examples to include strategy-influence json sample", tc.topicName)
		}
		if !foundPath {
			t.Fatalf("expected %s examples to include strategy-influence path example", tc.topicName)
		}
		if !foundBasis {
			t.Fatalf("expected %s examples to include strategy-influence path basis", tc.topicName)
		}
	}
}

func TestCreateRemoveMigrateTopicsIncludeTextAndJSONExamples(t *testing.T) {
	for _, topicName := range []string{"create", "remove", "migrate"} {
		topic, ok := exampleCatalog[topicName]
		if !ok {
			t.Fatalf("expected %s topic in catalog", topicName)
		}
		if !topicHasTextAndJSONExamples(topic) {
			t.Fatalf("expected %s topic to include both text and json examples", topicName)
		}
	}
}

func TestRenderExamplesTextPrintsAllListItems(t *testing.T) {
	output := captureStdout(t, func() {
		renderExamplesText([]exampleTopic{
			{
				Name:        "demo",
				Description: "demo topic",
				Examples: []usageExample{
					{
						Command:       "wt demo",
						Outcome:       "demo outcome",
						ExitCode:      "0",
						Preconditions: []string{"pre one", "pre two"},
						FailureModes:  []string{"fail one", "fail two"},
						FollowUp:      []string{"follow one", "follow two"},
						Notes:         []string{"note one", "note two"},
					},
				},
			},
		})
	})

	for _, needle := range []string{
		"- pre one",
		"- pre two",
		"- fail one",
		"- fail two",
		"- follow one",
		"- follow two",
		"- note one",
		"- note two",
	} {
		if !strings.Contains(output, needle) {
			t.Fatalf("expected rendered examples to include %q\noutput:\n%s", needle, output)
		}
	}
}

func TestRenderExamplesTextIncludesStrategyPathExamples(t *testing.T) {
	output := captureStdout(t, func() {
		renderExamplesText([]exampleTopic{exampleCatalog["create"]})
	})

	for _, needle := range []string{"global:", "sibling-repo:", "parent-branches:", "parent-worktrees:", "custom pattern:"} {
		if !strings.Contains(output, needle) {
			t.Fatalf("expected rendered examples text to include %q\noutput:\n%s", needle, output)
		}
	}
}

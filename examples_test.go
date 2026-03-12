package main

import (
	"strings"
	"testing"
)

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

func TestListTextExampleLooksRawStyle(t *testing.T) {
	topic := exampleCatalog["list"]
	if len(topic.Examples) == 0 {
		t.Fatal("expected list examples")
	}
	if !strings.Contains(topic.Examples[0].TextExample, "[main]") {
		t.Fatal("expected raw-style branch marker in list text example")
	}
}

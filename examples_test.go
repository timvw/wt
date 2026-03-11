package main

import "testing"

func TestCanonicalTopicAlias(t *testing.T) {
	if got := canonicalTopic("rm"); got != "remove" {
		t.Fatalf("canonicalTopic(rm) = %q, want remove", got)
	}
	if got := canonicalTopic("co"); got != "checkout" {
		t.Fatalf("canonicalTopic(co) = %q, want checkout", got)
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

package main

import (
	"strings"

	"github.com/sahilm/fuzzy"
)

// fuzzyMatch returns true if input fuzzy-matches the target string (case-insensitive).
func fuzzyMatch(input, target string) bool {
	if input == "" {
		return true
	}
	matches := fuzzy.Find(strings.ToLower(input), []string{strings.ToLower(target)})
	return len(matches) > 0
}

// fuzzySearcher returns a Searcher function compatible with promptui.Select.Searcher.
// It checks whether the input fuzzy-matches the item at the given index.
func fuzzySearcher(items []string) func(input string, index int) bool {
	return func(input string, index int) bool {
		return fuzzyMatch(input, items[index])
	}
}

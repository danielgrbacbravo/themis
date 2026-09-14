package main

import (
	"testing"

	"themis-cli/internal/state"
)

func TestShouldUseLinkedRootAsTUIRoot(t *testing.T) {
	st := state.State{
		Nodes: map[string]state.Node{
			"url:cached": {ID: "url:cached"},
		},
	}

	if !shouldUseLinkedRootAsTUIRoot(st, "", "url:cached") {
		t.Fatalf("expected cached linked root to be used")
	}

	if shouldUseLinkedRootAsTUIRoot(st, "url:explicit", "url:cached") {
		t.Fatalf("expected explicit root to take precedence")
	}

	if shouldUseLinkedRootAsTUIRoot(st, "", "url:missing") {
		t.Fatalf("expected missing linked root to be ignored")
	}
}

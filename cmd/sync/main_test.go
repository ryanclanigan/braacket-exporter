package main

import (
	"braacketreplacement/internal/synccore"
	"testing"
)

func TestParryEventHasMultiplePhases(t *testing.T) {
	if parryEventHasMultiplePhases([]synccore.ParryBracketPreview{{Phase: "Main"}, {Phase: "Main"}}) {
		t.Fatal("expected one phase to avoid the required selection prompt")
	}
	if !parryEventHasMultiplePhases([]synccore.ParryBracketPreview{{Phase: "Main"}, {Phase: "Amateur"}}) {
		t.Fatal("expected multiple phases to require a selection")
	}
}

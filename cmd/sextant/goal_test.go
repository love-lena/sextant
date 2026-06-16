package main

import "testing"

func TestValidGoalState(t *testing.T) {
	// the ADR-0035 enum — exactly these five, nothing else.
	for _, s := range goalStates {
		if !validGoalState(s) {
			t.Errorf("expected %q to be a valid goal state", s)
		}
	}
	if len(goalStates) != 5 {
		t.Errorf("expected 5 goal states (pending|active|blocked|done|dropped), got %d: %v", len(goalStates), goalStates)
	}
	for _, s := range []string{"", "working", "idle", "Done", "open", "waiting-for-human"} {
		if validGoalState(s) {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}

func TestGoalName(t *testing.T) {
	if got := goalName("v0.4.0"); got != "goal.v0.4.0" {
		t.Errorf("goalName(\"v0.4.0\") = %q, want goal.v0.4.0", got)
	}
}

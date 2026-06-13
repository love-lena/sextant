package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWorkflowRoundTrip covers AC#3: the sextant.workflow/v1 record (status, owner,
// steps[]) marshals and parses back intact, stamping the versioned $type.
func TestWorkflowRoundTrip(t *testing.T) {
	in := Workflow{
		ID: "01WF", Status: wfRunning, Owner: "01OWNER",
		Steps: []Step{
			{ID: "review", Kind: "dispatch", Nickname: "reviewer", Prompt: "review it", Status: stepDone, Agent: "01AGENT"},
			{ID: "merge", Kind: "dispatch", Status: stepPending},
		},
	}
	got, ok := parseWorkflow(in.marshal())
	if !ok {
		t.Fatal("parseWorkflow returned false for a valid record")
	}
	if got.Type != kindWorkflow {
		t.Errorf("$type = %q, want %q", got.Type, kindWorkflow)
	}
	if got.ID != in.ID || got.Owner != in.Owner || len(got.Steps) != 2 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Steps[0].Agent != "01AGENT" || got.Steps[0].Status != stepDone || got.Steps[1].Status != stepPending {
		t.Errorf("step round-trip mismatch: %+v", got.Steps)
	}
}

// TestNextPendingSkipsDone covers the idempotent-resume core: nextPending returns
// the first not-done step, so a resumed coordinator skips completed work.
func TestNextPendingSkipsDone(t *testing.T) {
	cases := []struct {
		name  string
		steps []Step
		want  int
	}{
		{"fresh", []Step{{Status: stepPending}, {Status: stepPending}}, 0},
		{"one done", []Step{{Status: stepDone}, {Status: stepPending}}, 1},
		{"running is not done", []Step{{Status: stepDone}, {Status: stepRunning}}, 1},
		{"failed is not done", []Step{{Status: stepDone}, {Status: stepFailed}}, 1},
		{"all done", []Step{{Status: stepDone}, {Status: stepDone}}, -1},
		{"empty", nil, -1},
	}
	for _, tc := range cases {
		w := Workflow{Steps: tc.steps}
		if got := w.nextPending(); got != tc.want {
			t.Errorf("%s: nextPending = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestIsTerminal pins the resume guard: done/cancelled/failed are terminal (a
// resumed coordinator does nothing), running/paused/pending are not.
func TestIsTerminal(t *testing.T) {
	for _, s := range []string{wfDone, wfCancelled, wfFailed} {
		if !isTerminal(s) {
			t.Errorf("isTerminal(%q) = false, want true", s)
		}
	}
	for _, s := range []string{wfRunning, wfPaused, stepPending, ""} {
		if isTerminal(s) {
			t.Errorf("isTerminal(%q) = true, want false", s)
		}
	}
}

// TestEventAndControlRoundTrip covers the event + control records and their guards.
func TestEventAndControlRoundTrip(t *testing.T) {
	ev := WorkflowEvent{Step: "review", Status: stepDone, By: "01AGENT"}
	if got, ok := parseWorkflowEvent(ev.marshal()); !ok || got.Type != typeWorkflowEvent || got.Step != "review" || got.Status != stepDone {
		t.Errorf("event round-trip: ok=%v got=%+v", ok, got)
	}
	if _, ok := parseWorkflowEvent(json.RawMessage(`{"$type":"chat.message","text":"hi"}`)); ok {
		t.Error("parseWorkflowEvent accepted a non-event record")
	}
	ctl := json.RawMessage(`{"$type":"workflow.control","verb":"pause"}`)
	if got, ok := parseWorkflowControl(ctl); !ok || got.Verb != ctlPause {
		t.Errorf("control parse: ok=%v got=%+v", ok, got)
	}
	if _, ok := parseWorkflowControl(json.RawMessage(`{"$type":"workflow.event","status":"done"}`)); ok {
		t.Error("parseWorkflowControl accepted a non-control record")
	}
}

// TestSpawnAckParse covers the M5.2-composition correlation: a spawn.ack parses and
// a spawn.request (or other $type) is rejected.
func TestSpawnAckParse(t *testing.T) {
	ack := json.RawMessage(`{"$type":"spawn.ack","id":"01CHILD","requestId":"01REQ","status":"ok"}`)
	if got, ok := parseSpawnAck(ack); !ok || got.ID != "01CHILD" || got.RequestID != "01REQ" || got.Status != "ok" {
		t.Errorf("spawn.ack parse: ok=%v got=%+v", ok, got)
	}
	if _, ok := parseSpawnAck(json.RawMessage(`{"$type":"spawn.request","prompt":"x"}`)); ok {
		t.Error("parseSpawnAck accepted a spawn.request")
	}
}

// TestWorkflowLexiconsParse covers AC#1's contract artifacts: the workflow lexicon
// files exist, parse, and declare the expected ids.
func TestWorkflowLexiconsParse(t *testing.T) {
	for _, want := range []string{"sextant.workflow", "workflow.event", "workflow.control"} {
		path := filepath.Join("..", "..", "protocol", "lexicons", want+".json")
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var lex struct {
			ID   string         `json:"id"`
			Defs map[string]any `json:"defs"`
		}
		if err := json.Unmarshal(b, &lex); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if lex.ID != want {
			t.Errorf("%s: id = %q, want %q", path, lex.ID, want)
		}
		if _, ok := lex.Defs["main"]; !ok {
			t.Errorf("%s: missing defs.main", path)
		}
	}
}

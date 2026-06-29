package workflow

import (
	"encoding/json"
	"testing"
)

func TestRunRoundTrip(t *testing.T) {
	r := Run{
		ID:        "01HRUN",
		Template:  nil,
		Label:     "do the thing",
		Objective: "do the whole thing",
		Status:    RunRunning,
		Steps: []RunStep{
			{ID: "s1", Label: "investigate", Kind: KindWork, Status: StepRunning},
			{ID: "brief", Label: "stopping brief", Kind: KindBrief, Status: StepUpcoming},
		},
		Relates:  []RelatesLink{{Goal: "g1", Crit: "c1", Kind: "toward"}},
		Activity: []ActivityEntry{{ID: "a1", Glyph: "•", Text: "spawned", Src: "01HRUN", At: 123}},
		Stop:     []string{"done — brief w/ proof", "blocked — brief"},
		Created:  123,
	}
	got, ok := ParseRun(r.Marshal())
	if !ok {
		t.Fatal("ParseRun rejected a valid run")
	}
	if got.Status != RunRunning || len(got.Steps) != 2 || got.Steps[1].Kind != KindBrief {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if string(r.Marshal()[:len(`{"$type":"sextant.workflow.run/v1"`)]) != `{"$type":"sextant.workflow.run/v1"` {
		t.Fatalf("run $type not stamped first: %s", r.Marshal())
	}
}

func TestParseRunRejectsWrongType(t *testing.T) {
	if _, ok := ParseRun(json.RawMessage(`{"$type":"sextant.workflow/v1","id":"x"}`)); ok {
		t.Fatal("ParseRun accepted the OLD type")
	}
}

// A null template marshals as explicit null (ad-hoc run) and round-trips as nil.
func TestRunTemplateNullable(t *testing.T) {
	r := Run{ID: "01H", Status: RunRunning}
	if got, ok := ParseRun(r.Marshal()); !ok || got.Template != nil {
		t.Fatalf("ad-hoc run template = %v, want nil (ok=%v)", got.Template, ok)
	}
	name := "review-changes"
	r2 := Run{ID: "01H", Status: RunRunning, Template: &name}
	got, ok := ParseRun(r2.Marshal())
	if !ok || got.Template == nil || *got.Template != name {
		t.Fatalf("templated run template = %v, want %q", got.Template, name)
	}
}

func TestRunNextPendingSkipsDone(t *testing.T) {
	r := Run{Steps: []RunStep{
		{ID: "s1", Status: StepDone}, {ID: "s2", Status: StepUpcoming}, {ID: "s3", Status: StepUpcoming},
	}}
	if got := r.NextPending(); got != 1 {
		t.Fatalf("NextPending = %d, want 1", got)
	}
	r.Steps[1].Status = StepDone
	r.Steps[2].Status = StepDone
	if got := r.NextPending(); got != -1 {
		t.Fatalf("NextPending = %d, want -1 (all done)", got)
	}
}

func TestRunEventRoundTrip(t *testing.T) {
	e := RunEvent{
		Step: "s1", Status: StepDone, By: "agent-1", Outcome: "done",
		Artifacts: []ProducedArtifact{{Name: "plan-x", Kind: "plan", Version: 1}},
	}
	got, ok := ParseRunEvent(e.Marshal())
	if !ok || got.Step != "s1" || got.Outcome != "done" || len(got.Artifacts) != 1 {
		t.Fatalf("run.event round-trip mismatch: %+v ok=%v", got, ok)
	}
	if _, ok := ParseRunEvent(json.RawMessage(`{"$type":"chat.message","text":"hi"}`)); ok {
		t.Error("ParseRunEvent accepted a non-event record")
	}
}

func TestParseRunControl(t *testing.T) {
	if got, ok := ParseRunControl(json.RawMessage(`{"$type":"run.control","verb":"approve"}`)); !ok || got.Verb != CtlApprove {
		t.Fatalf("ParseRunControl good: %+v ok=%v", got, ok)
	}
	if _, ok := ParseRunControl(json.RawMessage(`{"$type":"run.event","status":"done"}`)); ok {
		t.Fatal("ParseRunControl accepted a non-control record")
	}
}

func TestParseRunStartRequest(t *testing.T) {
	good := json.RawMessage(`{"$type":"run.start","id":"01HRUN","nonce":"n1"}`)
	r, ok := ParseRunStartRequest(good)
	if !ok || r.ID != "01HRUN" || r.Nonce != "n1" {
		t.Fatalf("ParseRunStartRequest good: %+v ok=%v", r, ok)
	}
	if _, ok := ParseRunStartRequest(json.RawMessage(`{"$type":"run.start","id":""}`)); ok {
		t.Fatal("ParseRunStartRequest accepted an empty id")
	}
	if _, ok := ParseRunStartRequest(json.RawMessage(`{"$type":"chat.message","text":"hi"}`)); ok {
		t.Fatal("ParseRunStartRequest accepted a non-run.start type")
	}
}

func TestRunStartAckRoundTrip(t *testing.T) {
	a := RunStartAck{ID: "01HRUN", Nonce: "n1", RequestID: "01REQ", Status: StatusOK}
	var got RunStartAck
	if err := json.Unmarshal(a.Marshal(), &got); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if got.Type != TypeRunStartAck || got.ID != "01HRUN" || got.RequestID != "01REQ" || got.Status != StatusOK {
		t.Fatalf("ack round-trip mismatch: %+v", got)
	}
}

func TestRunSubjects(t *testing.T) {
	if got := RunStateName("01H"); got != "workflow.run.01H" {
		t.Errorf("RunStateName = %q", got)
	}
	if got := RunEventsSubject("01H"); got != "msg.workflow.run.01H.events" {
		t.Errorf("RunEventsSubject = %q", got)
	}
	if got := RunControlSubject("01H"); got != "msg.workflow.run.01H.control" {
		t.Errorf("RunControlSubject = %q", got)
	}
}

func TestIsTerminalRun(t *testing.T) {
	for _, s := range []string{RunDone, RunBlocked, RunCancelled} {
		if !IsTerminalRun(s) {
			t.Errorf("IsTerminalRun(%q) = false, want true", s)
		}
	}
	for _, s := range []string{RunRunning, RunWaiting} {
		if IsTerminalRun(s) {
			t.Errorf("IsTerminalRun(%q) = true, want false", s)
		}
	}
}

// (TASK-243 option A) The brief-body proof extractor was removed: the deterministic
// stop-gate no longer parses the brief's content for proof refs under any key set. It
// decides solely from the TYPED produced-artifact metadata the worker reports in its
// run.event (existence-checked by the coordinator). The gate's behaviour is proven
// adversarially against a real bus in clients/coordinator (TestRun_BlocksOnFabricatedProof,
// TestRun_DoneWithDeliverableUnderNovelBriefKey).

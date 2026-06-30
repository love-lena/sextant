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
			{ID: "verify", Label: "verify the deliverable", Kind: KindVerify, Status: StepUpcoming},
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
	if got.Status != RunRunning || len(got.Steps) != 3 || got.Steps[1].Kind != KindVerify || got.Steps[2].Kind != KindBrief {
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

// TestPROpenStepRoundTrips (TASK-260 AC#3): a dev-workflow definition carrying a pr-open
// step round-trips through both the Run and Template envelopes with the kind preserved, so
// the standard TASK-98 template can declare the trusted-path PR-open step (not a manual
// operator step). The kind string is co-equal with the TS mirror (conventions/workflow/ts).
func TestPROpenStepRoundTrips(t *testing.T) {
	if KindPROpen != "pr-open" {
		t.Fatalf("KindPROpen = %q; the wire value must be %q (co-equal with the TS mirror)", KindPROpen, "pr-open")
	}
	// The standard dev-workflow shape: build → review → open PR → stopping brief.
	r := Run{
		ID: "01HPR", Status: RunRunning,
		Steps: []RunStep{
			{ID: "s1", Label: "build", Kind: KindWork, Status: StepRunning},
			{ID: "s2", Label: "review", Kind: KindWork, Status: StepUpcoming},
			{ID: "pr", Label: "open a pull request", Kind: KindPROpen, Status: StepUpcoming},
			{ID: "brief", Label: "stopping brief", Kind: KindBrief, Status: StepUpcoming},
		},
	}
	got, ok := ParseRun(r.Marshal())
	if !ok {
		t.Fatal("ParseRun rejected a run with a pr-open step")
	}
	if got.Steps[2].Kind != KindPROpen {
		t.Fatalf("pr-open step kind not preserved: %+v", got.Steps[2])
	}

	tpl := Template{
		Name: "Plan → build → review → PR",
		Steps: []TemplateStep{
			{ID: "s1", Label: "build", Kind: KindWork},
			{ID: "pr", Label: "open a pull request", Kind: KindPROpen},
			{ID: "brief", Label: "stopping brief", Kind: KindBrief},
		},
	}
	b, _ := json.Marshal(tpl)
	var back Template
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("template with a pr-open step did not round-trip: %v", err)
	}
	if back.Steps[1].Kind != KindPROpen {
		t.Fatalf("template pr-open step kind not preserved: %+v", back.Steps[1])
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
	if got := RunReviewSubject("01H"); got != "msg.workflow.run.01H.review" {
		t.Errorf("RunReviewSubject = %q", got)
	}
	if got := RunDecisionSubject("01H"); got != "msg.workflow.run.01H.decision" {
		t.Errorf("RunDecisionSubject = %q", got)
	}
}

// TestAgentModeRecords pins the agent-mode lexicon (TASK-242): the run.review request and
// run.decision reply round-trip stamping their $types and reject foreign records; the run
// envelope carries agent_mode; and IsDecisionVerb recognises EXACTLY the four v1 verbs and
// rejects graph reshaping (branch/insert/skip) — the guard that keeps the shell from
// advancing on an out-of-vocabulary verb.
func TestAgentModeRecords(t *testing.T) {
	review := RunReview{Step: "s1", Objective: "obj", Produced: []ProducedArtifact{{Name: "a", Kind: "work"}}}
	got, ok := ParseRunReview(review.Marshal())
	if !ok || got.Type != TypeRunReview || got.Step != "s1" || len(got.Produced) != 1 || got.Produced[0].Name != "a" {
		t.Fatalf("RunReview round-trip mismatch: %+v ok=%v", got, ok)
	}
	if _, ok := ParseRunReview(json.RawMessage(`{"$type":"run.event","status":"done"}`)); ok {
		t.Fatal("ParseRunReview accepted a non-review record")
	}

	dec := RunDecision{Step: "s1", Verb: DecisionRedo, Feedback: "fix it", Reason: "wrong"}
	gd, ok := ParseRunDecision(dec.Marshal())
	if !ok || gd.Type != TypeRunDecision || gd.Verb != DecisionRedo || gd.Feedback != "fix it" {
		t.Fatalf("RunDecision round-trip mismatch: %+v ok=%v", gd, ok)
	}
	if _, ok := ParseRunDecision(json.RawMessage(`{"$type":"run.review","step":"s1"}`)); ok {
		t.Fatal("ParseRunDecision accepted a non-decision record")
	}

	for _, v := range []string{DecisionAdvance, DecisionRedo, DecisionEdit, DecisionStop} {
		if !IsDecisionVerb(v) {
			t.Errorf("IsDecisionVerb(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"branch", "insert", "skip", "", "ADVANCE"} {
		if IsDecisionVerb(v) {
			t.Errorf("IsDecisionVerb(%q) = true, want false (no graph reshaping in v1)", v)
		}
	}

	r := Run{ID: "01H", AgentMode: true, Status: RunRunning}
	rr, ok := ParseRun(r.Marshal())
	if !ok || !rr.AgentMode {
		t.Fatalf("Run.AgentMode did not round-trip: %+v ok=%v", rr, ok)
	}
	// agent_mode is omitempty: a default run marshals WITHOUT the field (byte-compat).
	if b := (Run{ID: "01H", Status: RunRunning}).Marshal(); contains(string(b), "agent_mode") {
		t.Errorf("default run marshalled agent_mode (should be omitempty): %s", b)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
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

// TestRunStepModelRoundTrip pins TASK-245: the Model field on RunStep marshals
// and round-trips correctly, carrying the per-step model declaration from a run
// template through to the dispatcher.
//
// This is the CONTRACT layer of the AC#1 flow proof: RunStep.Model → (coordinator
// reads it) → SpawnRequest.Model → (dispatcher receives it) → SX_AGENT_MODEL.
// The env-var relay end is proven in clients/dispatcher/model_test.go.
func TestRunStepModelRoundTrip(t *testing.T) {
	r := Run{
		ID:     "01HMODEL",
		Status: RunRunning,
		Steps: []RunStep{
			{ID: "s1", Label: "write draft", Kind: KindWork, Status: StepUpcoming, Model: "claude-opus-4-5"},
			{ID: "s2", Label: "review draft", Kind: KindWork, Status: StepUpcoming, Model: "claude-sonnet-4-5"},
			{ID: "s3", Label: "finalize", Kind: KindWork, Status: StepUpcoming},
		},
	}
	got, ok := ParseRun(r.Marshal())
	if !ok {
		t.Fatal("ParseRun rejected a valid run with Model-bearing steps")
	}
	if got.Steps[0].Model != "claude-opus-4-5" {
		t.Errorf("step[0].Model = %q, want %q", got.Steps[0].Model, "claude-opus-4-5")
	}
	if got.Steps[1].Model != "claude-sonnet-4-5" {
		t.Errorf("step[1].Model = %q, want %q", got.Steps[1].Model, "claude-sonnet-4-5")
	}
	// A step with no declared model must marshal as omitted (omitempty), not as
	// an explicit empty string — no default baked into the convention layer.
	if got.Steps[2].Model != "" {
		t.Errorf("step[2].Model = %q, want empty (no declared model)", got.Steps[2].Model)
	}
	// Adversarial: the model field must be PRESENT in the marshalled JSON for
	// steps that declared one, and ABSENT for those that did not.
	b := r.Marshal()
	raw := string(b)
	if !contains(raw, `"claude-opus-4-5"`) {
		t.Errorf("marshalled run does not contain the declared model: %s", raw)
	}
	// omitempty guard: a step with no model must not emit the key at all
	// (prevents a consumer from confusing "" with "no model").
	for _, s := range got.Steps {
		if s.Model == "" && contains(raw, `"model":""`) {
			t.Errorf("step with no model emitted an explicit empty model key — must be omitted: %s", raw)
		}
	}
}

// TestSpawnRequestModelRoundTrip pins TASK-245 at the coordinator→dispatcher
// boundary: SpawnRequest.Model carries the step's declared model and survives
// a JSON round-trip. This is the WIRE shape the coordinator publishes and the
// dispatcher receives.
func TestSpawnRequestModelRoundTrip(t *testing.T) {
	req := SpawnRequest{Prompt: "do the work", Nickname: "writer", Job: "01H", Model: "claude-opus-4-5"}
	b := req.Marshal()

	// Unmarshal as a raw map to verify the wire shape.
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("SpawnRequest.Marshal produced invalid JSON: %v", err)
	}
	if m["model"] != "claude-opus-4-5" {
		t.Fatalf("SpawnRequest wire shape missing model: %v", m)
	}
	// Round-trip: parse back as SpawnAck is not relevant, but we can verify
	// the JSON key name matches what the dispatcher's spawn.ParseSpawnRequest reads.
	// The dispatcher uses conventions/spawn/go which has its own SpawnRequest;
	// the workflow.SpawnRequest must produce the same "model" key so the
	// dispatcher (which uses spawn.ParseSpawnRequest) reads it.
	if !contains(string(b), `"model":"claude-opus-4-5"`) {
		t.Errorf("SpawnRequest JSON key must be \"model\", got: %s", b)
	}

	// A request with no model must not emit the key (omitempty).
	noModel := SpawnRequest{Prompt: "do the work", Job: "01H"}
	nb := noModel.Marshal()
	if contains(string(nb), `"model"`) {
		t.Errorf("SpawnRequest with no model emitted \"model\" key (must be omitted): %s", nb)
	}
}

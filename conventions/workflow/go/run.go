package workflow

import (
	"context"
	"encoding/json"
	"fmt"
)

// The run contract (ADR-0048) — the run-record half of the workflow convention.
// A RUN is one live instance of work (a ULID with steps); a TEMPLATE is the
// reusable, generic spec. Both are CONVENTIONS over the two primitives (Messages +
// Artifacts), no engine in core: the run envelope is a regular Artifact
// (CAS-checkpointed, single-writer = the coordinator that drives it); control and
// events ride msg.* subjects.
//
// This is the live work-engine contract (ADR-0048), driven by clients/coordinator.
// The step statuses, control verbs, and the spawn lexicon it composes are shared
// from records.go.
const (
	KindRun         = "sextant.workflow.run/v1"
	KindTemplate    = "sextant.workflow.template/v1"
	TypeRunEvent    = "run.event"
	TypeRunControl  = "run.control"
	TypeRunStart    = "run.start"
	TypeRunStartAck = "run.start.ack"
)

// Run statuses (the dash's RUN_STATUS set; no "failed" — a failed step → blocked).
const (
	RunRunning   = "running"
	RunWaiting   = "waiting"
	RunBlocked   = "blocked"
	RunDone      = "done"
	RunCancelled = "cancelled"
)

// Step statuses unique to the run contract. StepRunning/StepDone are shared from
// records.go and reused. Kinds (ADR-0048) are run-specific.
const (
	StepUpcoming = "upcoming"
	StepWaiting  = "waiting"

	KindWork       = "work"
	KindCheckpoint = "checkpoint"
	KindBrief      = "brief"
)

// (Control verbs CtlApprove/CtlResume/CtlPause/CtlCancel and StatusOK/StatusError
// are declared in records.go and shared — the run contract reuses them verbatim.)

// IsTerminalRun reports whether a run status is final (done/blocked/cancelled).
func IsTerminalRun(status string) bool {
	return status == RunDone || status == RunBlocked || status == RunCancelled
}

// Run is the sextant.workflow.run/v1 state envelope: single-writer (the coordinator),
// CAS-guarded, stored as an Artifact keyed RunStateName(id). Steps are a flat status
// list (no DAG); activity is the embedded observability stream the dash polls.
type Run struct {
	Type      string             `json:"$type"`
	ID        string             `json:"id"`
	Template  *string            `json:"template"` // nil/null = ad-hoc; pointer to preserve explicit null
	Label     string             `json:"label,omitempty"`
	Objective string             `json:"objective,omitempty"`
	Status    string             `json:"status"`
	Steps     []RunStep          `json:"steps"`
	Relates   []RelatesLink      `json:"relates"`
	Activity  []ActivityEntry    `json:"activity"`
	Artifacts []ProducedArtifact `json:"artifacts"`
	Stop      []string           `json:"stop,omitempty"`
	Created   int64              `json:"created,omitempty"`
	Owner     string             `json:"owner,omitempty"` // coordinator client id (set on adopt)
}

// RunStep is one unit of work. Kind work → dispatch an agent; checkpoint → pause for
// the operator; brief → write the terminal stopping brief. Agent is filled from the
// spawn.ack for work/brief steps.
//
// Produced holds the artifact REFS (name/kind/version — never content) the step's
// worker reported in its run.event. It is the per-step deliverable record: AC#2 (each
// work step attaches >=1 distinct artifact) is checked against it, and AC#1 (piping)
// threads a later step's prompt against the refs prior steps produced. The coordinator
// stores and forwards these refs only; it never reads their content to thread them
// (content-opacity, AC#3) — the downstream worker dereferences the ref itself.
type RunStep struct {
	ID       string             `json:"id"`
	Label    string             `json:"label,omitempty"`
	Kind     string             `json:"kind"`
	Status   string             `json:"status"`
	Agent    string             `json:"agent,omitempty"`
	Produced []ProducedArtifact `json:"produced,omitempty"`
}

// RelatesLink binds a run to a goal/criterion (ADR-0035 relates, ADR-0048 toward).
type RelatesLink struct {
	Goal string `json:"goal"`
	Crit string `json:"crit,omitempty"`
	Kind string `json:"kind"` // "toward" | "proof" | "related"
}

// ActivityEntry is one row of the run's embedded, low-volume progress stream (the
// coordinator's milestones — distinct from the high-volume agent.activity feed).
type ActivityEntry struct {
	ID    string `json:"id"`
	Glyph string `json:"glyph,omitempty"`
	Text  string `json:"text"`
	Src   string `json:"src,omitempty"`
	At    int64  `json:"at"`
}

// ProducedArtifact is an artifact a step produced, attached to the run (ADR-0048).
type ProducedArtifact struct {
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"`
	Version int    `json:"version,omitempty"`
	Status  string `json:"status,omitempty"`
}

// RunEvent is the agent→coordinator step-done signal on RunEventsSubject(id). A
// dispatched agent emits {step, status:"done", by, outcome?, artifacts?}; the
// coordinator reads it, attaches artifacts, appends activity, and advances.
type RunEvent struct {
	Type      string             `json:"$type"`
	Step      string             `json:"step,omitempty"`
	Status    string             `json:"status"`
	By        string             `json:"by,omitempty"`
	Note      string             `json:"note,omitempty"`
	Outcome   string             `json:"outcome,omitempty"` // brief step: "done" | "blocked"
	Artifacts []ProducedArtifact `json:"artifacts,omitempty"`
}

// DeclaredProofArtifacts extracts the artifact NAMES a brief record claims as its
// proof / deliverables, from the convention-recognized fields: the canonical
// `proof_artifacts` array, plus the two shapes models reach for unprompted
// (`proof_of_completion.artifact` and `artifacts_to_report[].name`). The
// coordinator existence-checks each before letting a run go `done`, so a brief
// cannot certify completion against an artifact that was never produced — the
// fabricated-proof stop gate (TASK-243; a run literally did this for a poem that
// did not exist). It is best-effort over a free-form brief body: an unrecognized
// shape yields nothing, so the gate then only checks the declared produced
// artifacts. The coordinator (a deterministic client, not the AI worker) reading
// the brief it manages is ordinary convention logic — content-opacity constrains
// the core/bus, not a convention interpreting its own records.
func DeclaredProofArtifacts(record json.RawMessage) []string {
	var b struct {
		ProofArtifacts    []string `json:"proof_artifacts"`
		ProofOfCompletion struct {
			Artifact string `json:"artifact"`
		} `json:"proof_of_completion"`
		ArtifactsToReport []struct {
			Name string `json:"name"`
		} `json:"artifacts_to_report"`
	}
	if err := json.Unmarshal(record, &b); err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	for _, n := range b.ProofArtifacts {
		add(n)
	}
	add(b.ProofOfCompletion.Artifact)
	for _, a := range b.ArtifactsToReport {
		add(a.Name)
	}
	return out
}

// RunControl is a cooperative control message on RunControlSubject(id).
type RunControl struct {
	Type string `json:"$type"`
	Verb string `json:"verb"`
}

// RunStartRequest wakes the coordinator: the dash has written the run artifact and
// asks the coordinator to adopt it by id. ID is required; Nonce is the dash's opaque
// correlation handle echoed in the ack.
type RunStartRequest struct {
	Type  string `json:"$type"`
	ID    string `json:"id"`
	Nonce string `json:"nonce,omitempty"`
}

// RunStartAck is published back on RunStartSubject for every handled request (fail-loud).
type RunStartAck struct {
	Type      string `json:"$type"`
	ID        string `json:"id,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// Template is the sextant.workflow.template/v1 reusable workflow (matches the dash's
// buildRecord at workengine.jsx). Carries no goal/criterion (a template is generic).
type Template struct {
	Type           string         `json:"$type"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	Steps          []TemplateStep `json:"steps"`
	Triggers       []any          `json:"triggers,omitempty"`
	StopConditions []string       `json:"stop_conditions,omitempty"`
}

type TemplateStep struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Kind  string `json:"kind"`
}

func (r Run) Marshal() json.RawMessage {
	r.Type = KindRun
	b, _ := json.Marshal(r)
	return b
}

func ParseRun(record json.RawMessage) (Run, bool) {
	var r Run
	if err := json.Unmarshal(record, &r); err != nil || r.Type != KindRun {
		return Run{}, false
	}
	return r, true
}

// NextPending returns the index of the first step not yet done (skipping done steps —
// what makes a resumed coordinator idempotent), or -1 if all are done.
func (r *Run) NextPending() int {
	for i := range r.Steps {
		if r.Steps[i].Status != StepDone {
			return i
		}
	}
	return -1
}

func (e RunEvent) Marshal() json.RawMessage {
	e.Type = TypeRunEvent
	b, _ := json.Marshal(e)
	return b
}

func ParseRunEvent(record json.RawMessage) (RunEvent, bool) {
	var e RunEvent
	if err := json.Unmarshal(record, &e); err != nil || e.Type != TypeRunEvent {
		return RunEvent{}, false
	}
	return e, true
}

func (c RunControl) Marshal() json.RawMessage {
	c.Type = TypeRunControl
	b, _ := json.Marshal(c)
	return b
}

func ParseRunControl(record json.RawMessage) (RunControl, bool) {
	var c RunControl
	if err := json.Unmarshal(record, &c); err != nil || c.Type != TypeRunControl {
		return RunControl{}, false
	}
	return c, true
}

func ParseRunStartRequest(record json.RawMessage) (RunStartRequest, bool) {
	var r RunStartRequest
	if err := json.Unmarshal(record, &r); err != nil {
		return RunStartRequest{}, false
	}
	if r.Type != TypeRunStart || r.ID == "" {
		return RunStartRequest{}, false
	}
	return r, true
}

func (a RunStartAck) Marshal() json.RawMessage {
	a.Type = TypeRunStartAck
	b, _ := json.Marshal(a)
	return b
}

// Convention subjects + state-artifact name for the run contract (msg.* + ARTIFACTS).
func RunStateName(id string) string      { return "workflow.run." + id }
func RunEventsSubject(id string) string  { return "msg.workflow.run." + id + ".events" }
func RunControlSubject(id string) string { return "msg.workflow.run." + id + ".control" }

// RunStartSubject is the well-known subject the coordinator watches for run.start.
const RunStartSubject = "msg.topic.run.start"

// RunStartRecord renders a run.start request as a canonical record payload (single
// source of the wire shape; the Go verb and the TS peer emit byte-identical bytes).
func RunStartRecord(req RunStartRequest) json.RawMessage {
	req.Type = TypeRunStart
	b, _ := json.Marshal(req)
	return b
}

// RequestRunStart publishes a run.start on RunStartSubject — the one bus operation a
// requester (the dash) issues to ask the coordinator to adopt a run it just wrote.
func RequestRunStart(ctx context.Context, ops Ops, req RunStartRequest) error {
	if err := ops.Publish(ctx, RunStartSubject, RunStartRecord(req)); err != nil {
		return fmt.Errorf("workflow: publish run.start on %s: %w", RunStartSubject, err)
	}
	return nil
}

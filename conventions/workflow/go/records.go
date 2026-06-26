package workflow

import "encoding/json"

// The workflow lexicon (ADR-0011). A workflow is a CONVENTION over the two
// primitives, run by an ordinary coordinator client — no engine in core. Layer-0
// is an id + addressing: the state envelope is a regular Artifact (CAS-checkpointed)
// and control/events ride msg.* subjects. (The reserved sx.workflow.* subjects and
// sx_workflows bucket are not client-reachable through the current Wire API — a
// client publishes only to msg.* and writes only the ARTIFACTS bucket — so the
// coordinator realizes Layer-0 over the primitives it actually has, which is exactly
// ADR-0011's "convention over Messages + Artifacts". See m5-workflow-notes.md.)
const (
	// KindWorkflow is the Layer-1 reference state record, versioned in its $type so
	// it evolves without an epoch bump (ADR-0011).
	KindWorkflow        = "sextant.workflow/v1"
	TypeWorkflowEvent   = "workflow.event"
	TypeWorkflowControl = "workflow.control"
)

// Workflow statuses and step statuses.
const (
	WfRunning   = "running"
	WfPaused    = "paused"
	WfDone      = "done"
	WfCancelled = "cancelled"
	WfFailed    = "failed"

	StepPending = "pending"
	StepRunning = "running"
	StepDone    = "done"
	StepFailed  = "failed"
)

// Control verbs (cooperative — they ask, they don't compel; ADR-0011).
const (
	CtlPause   = "pause"
	CtlResume  = "resume"
	CtlCancel  = "cancel"
	CtlApprove = "approve"
)

// IsTerminal reports whether a workflow status is final (done/cancelled/failed):
// a resumed coordinator does nothing for a workflow already in a terminal state.
func IsTerminal(status string) bool {
	return status == WfDone || status == WfCancelled || status == WfFailed
}

// Workflow is the sextant.workflow/v1 state envelope: single-writer (the owner),
// CAS-guarded, with steps as a flat status list (no transition logic in the
// substrate — the coordinator walks it). Stored as an Artifact keyed by id.
type Workflow struct {
	Type   string `json:"$type"`
	ID     string `json:"id"`
	Status string `json:"status"`
	Owner  string `json:"owner"`
	Steps  []Step `json:"steps"`
}

// Step is one unit of work. Kind "dispatch" stands up an agent (composing M5.2):
// the coordinator publishes a spawn.request carrying Prompt and the work is done
// when the agent reports a step-done workflow.event. Agent is filled from the
// spawn.ack once the child is minted.
type Step struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Nickname string `json:"nickname,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	Status   string `json:"status"`
	Agent    string `json:"agent,omitempty"`
}

// WorkflowEvent is the free-form history stream alongside the state envelope
// (ADR-0011), on msg.workflow.<id>.events. The coordinator emits step/status
// transitions; a dispatched agent emits its own step-done signal here.
type WorkflowEvent struct {
	Type   string `json:"$type"`
	Step   string `json:"step,omitempty"`
	Status string `json:"status"`
	By     string `json:"by,omitempty"`
	Note   string `json:"note,omitempty"`
}

// WorkflowControl is a cooperative control message on msg.workflow.<id>.control.
type WorkflowControl struct {
	Type string `json:"$type"`
	Verb string `json:"verb"`
}

// NextPending returns the index of the first step that is not yet done (skipping
// done steps — this is what makes a resumed coordinator idempotent), or -1 if all
// steps are done.
func (w *Workflow) NextPending() int {
	for i := range w.Steps {
		if w.Steps[i].Status != StepDone {
			return i
		}
	}
	return -1
}

func (w *Workflow) Marshal() json.RawMessage {
	w.Type = KindWorkflow
	b, _ := json.Marshal(w)
	return b
}

func ParseWorkflow(record json.RawMessage) (Workflow, bool) {
	var w Workflow
	if err := json.Unmarshal(record, &w); err != nil || w.Type != KindWorkflow {
		return Workflow{}, false
	}
	return w, true
}

func (e WorkflowEvent) Marshal() json.RawMessage {
	e.Type = TypeWorkflowEvent
	b, _ := json.Marshal(e)
	return b
}

func ParseWorkflowEvent(record json.RawMessage) (WorkflowEvent, bool) {
	var e WorkflowEvent
	if err := json.Unmarshal(record, &e); err != nil || e.Type != TypeWorkflowEvent {
		return WorkflowEvent{}, false
	}
	return e, true
}

func ParseWorkflowControl(record json.RawMessage) (WorkflowControl, bool) {
	var c WorkflowControl
	if err := json.Unmarshal(record, &c); err != nil || c.Type != TypeWorkflowControl {
		return WorkflowControl{}, false
	}
	return c, true
}

// Convention subjects + state-artifact name (msg.* space + ARTIFACTS bucket — the
// client-reachable realization of ADR-0011's Layer-0 addressing).
func StateName(id string) string      { return "workflow." + id }
func EventsSubject(id string) string  { return "msg.workflow." + id + ".events" }
func ControlSubject(id string) string { return "msg.workflow." + id + ".control" }

// StartSubject is the well-known subject the workflow consumer watches for
// workflow.start requests and publishes workflow.start.ack to (mirrors the
// dispatcher's msg.topic.spawn pattern, ADR-0011 / violet mobilizer).
const StartSubject = "msg.topic.workflow.start"

// workflow.start / workflow.start.ack lexicon.
const (
	TypeWorkflowStart    = "workflow.start"
	TypeWorkflowStartAck = "workflow.start.ack"

	StatusOK    = "ok"
	StatusError = "error"
)

// WorkflowStartRequest is the record published on msg.topic.workflow.start to
// ask the coordinator to start a new run. Prompt is required; Nonce is the
// dash's opaque correlation handle (echoed verbatim in the ack so the dash can
// correlate without knowing its own bus Frame.ID at publish time); Nickname,
// Target, and By are informational labels.
type WorkflowStartRequest struct {
	Type     string `json:"$type"`
	Prompt   string `json:"prompt"`
	Nonce    string `json:"nonce,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Target   string `json:"target,omitempty"`
	By       string `json:"by,omitempty"`
}

// WorkflowStartAck is published back on StartSubject for every handled request
// (success or failure) — fail-loud: the requester is never left waiting on
// silence. Nonce echoes the request's nonce verbatim (the dash's correlation
// handle). Mirrors spawn.ack (cmd/sextant-dispatch/records.go).
type WorkflowStartAck struct {
	Type       string `json:"$type"`
	Nonce      string `json:"nonce,omitempty"`
	RequestID  string `json:"requestId"`
	WorkflowID string `json:"workflowId,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

// ParseWorkflowStartRequest decodes a frame record as a workflow.start,
// returning false for any other $type or a request missing the required prompt.
// The consumer ignores everything it returns false for (mirrors parseSpawnRequest).
func ParseWorkflowStartRequest(record json.RawMessage) (WorkflowStartRequest, bool) {
	var r WorkflowStartRequest
	if err := json.Unmarshal(record, &r); err != nil {
		return WorkflowStartRequest{}, false
	}
	if r.Type != TypeWorkflowStart || r.Prompt == "" {
		return WorkflowStartRequest{}, false
	}
	return r, true
}

func (a WorkflowStartAck) Marshal() json.RawMessage {
	a.Type = TypeWorkflowStartAck
	b, _ := json.Marshal(a)
	return b
}

// Minimal mirror of the spawn lexicon (the contract is protocol/lexicons/spawn.*.json;
// cmd/sextant-dispatch is the authoritative impl). A dispatch step COMPOSES M5.2 by
// publishing a spawn.request and correlating the dispatcher's spawn.ack by requestId.
const (
	TypeSpawnRequest = "spawn.request"
	TypeSpawnAck     = "spawn.ack"
)

type SpawnRequest struct {
	Type     string `json:"$type"`
	Prompt   string `json:"prompt"`
	Nickname string `json:"nickname,omitempty"`
	Job      string `json:"job,omitempty"`
}

func (r SpawnRequest) Marshal() json.RawMessage {
	r.Type = TypeSpawnRequest
	b, _ := json.Marshal(r)
	return b
}

type SpawnAck struct {
	Type      string `json:"$type"`
	ID        string `json:"id,omitempty"`
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

func ParseSpawnAck(record json.RawMessage) (SpawnAck, bool) {
	var a SpawnAck
	if err := json.Unmarshal(record, &a); err != nil || a.Type != TypeSpawnAck {
		return SpawnAck{}, false
	}
	return a, true
}

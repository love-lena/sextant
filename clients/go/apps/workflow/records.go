package main

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
	// kindWorkflow is the Layer-1 reference state record, versioned in its $type so
	// it evolves without an epoch bump (ADR-0011).
	kindWorkflow        = "sextant.workflow/v1"
	typeWorkflowEvent   = "workflow.event"
	typeWorkflowControl = "workflow.control"
)

// Workflow statuses and step statuses.
const (
	wfRunning   = "running"
	wfPaused    = "paused"
	wfDone      = "done"
	wfCancelled = "cancelled"
	wfFailed    = "failed"

	stepPending = "pending"
	stepRunning = "running"
	stepDone    = "done"
	stepFailed  = "failed"
)

// Control verbs (cooperative — they ask, they don't compel; ADR-0011).
const (
	ctlPause   = "pause"
	ctlResume  = "resume"
	ctlCancel  = "cancel"
	ctlApprove = "approve"
)

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

// nextPending returns the index of the first step that is not yet done (skipping
// done steps — this is what makes a resumed coordinator idempotent), or -1 if all
// steps are done.
func (w *Workflow) nextPending() int {
	for i := range w.Steps {
		if w.Steps[i].Status != stepDone {
			return i
		}
	}
	return -1
}

func (w *Workflow) marshal() json.RawMessage {
	w.Type = kindWorkflow
	b, _ := json.Marshal(w)
	return b
}

func parseWorkflow(record json.RawMessage) (Workflow, bool) {
	var w Workflow
	if err := json.Unmarshal(record, &w); err != nil || w.Type != kindWorkflow {
		return Workflow{}, false
	}
	return w, true
}

func (e WorkflowEvent) marshal() json.RawMessage {
	e.Type = typeWorkflowEvent
	b, _ := json.Marshal(e)
	return b
}

func parseWorkflowEvent(record json.RawMessage) (WorkflowEvent, bool) {
	var e WorkflowEvent
	if err := json.Unmarshal(record, &e); err != nil || e.Type != typeWorkflowEvent {
		return WorkflowEvent{}, false
	}
	return e, true
}

func parseWorkflowControl(record json.RawMessage) (WorkflowControl, bool) {
	var c WorkflowControl
	if err := json.Unmarshal(record, &c); err != nil || c.Type != typeWorkflowControl {
		return WorkflowControl{}, false
	}
	return c, true
}

// Convention subjects + state-artifact name (msg.* space + ARTIFACTS bucket — the
// client-reachable realization of ADR-0011's Layer-0 addressing).
func stateName(id string) string      { return "workflow." + id }
func eventsSubject(id string) string  { return "msg.workflow." + id + ".events" }
func controlSubject(id string) string { return "msg.workflow." + id + ".control" }

// startSubject is the well-known subject the workflow consumer watches for
// workflow.start requests and publishes workflow.start.ack to (mirrors the
// dispatcher's msg.topic.spawn pattern, ADR-0011 / violet mobilizer).
const startSubject = "msg.topic.workflow.start"

// workflow.start / workflow.start.ack lexicon.
const (
	typeWorkflowStart    = "workflow.start"
	typeWorkflowStartAck = "workflow.start.ack"

	statusOK    = "ok"
	statusError = "error"
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

// WorkflowStartAck is published back on startSubject for every handled request
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

// parseWorkflowStartRequest decodes a frame record as a workflow.start,
// returning false for any other $type or a request missing the required prompt.
// The consumer ignores everything it returns false for (mirrors parseSpawnRequest).
func parseWorkflowStartRequest(record json.RawMessage) (WorkflowStartRequest, bool) {
	var r WorkflowStartRequest
	if err := json.Unmarshal(record, &r); err != nil {
		return WorkflowStartRequest{}, false
	}
	if r.Type != typeWorkflowStart || r.Prompt == "" {
		return WorkflowStartRequest{}, false
	}
	return r, true
}

func (a WorkflowStartAck) marshal() json.RawMessage {
	a.Type = typeWorkflowStartAck
	b, _ := json.Marshal(a)
	return b
}

// Minimal mirror of the spawn lexicon (the contract is protocol/lexicons/spawn.*.json;
// cmd/sextant-dispatch is the authoritative impl). A dispatch step COMPOSES M5.2 by
// publishing a spawn.request and correlating the dispatcher's spawn.ack by requestId.
const (
	typeSpawnRequest = "spawn.request"
	typeSpawnAck     = "spawn.ack"
)

type spawnRequest struct {
	Type     string `json:"$type"`
	Prompt   string `json:"prompt"`
	Nickname string `json:"nickname,omitempty"`
	Job      string `json:"job,omitempty"`
}

func (r spawnRequest) marshal() json.RawMessage {
	r.Type = typeSpawnRequest
	b, _ := json.Marshal(r)
	return b
}

type spawnAck struct {
	Type      string `json:"$type"`
	ID        string `json:"id,omitempty"`
	RequestID string `json:"requestId"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

func parseSpawnAck(record json.RawMessage) (spawnAck, bool) {
	var a spawnAck
	if err := json.Unmarshal(record, &a); err != nil || a.Type != typeSpawnAck {
		return spawnAck{}, false
	}
	return a, true
}

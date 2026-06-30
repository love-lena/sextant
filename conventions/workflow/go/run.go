package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
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

	// Agent-mode review lexicon (TASK-242, ADR-0048-additive). In agent mode the
	// programmatic shell asks a long-lived coordinator AGENT to review each completed
	// step (run.review) and applies the agent's reply (run.decision). The shell stays the
	// SOLE single-writer of the run envelope: the agent NEVER writes the envelope, it only
	// emits a run.decision record the shell reads and applies. Both ride msg.* subjects.
	TypeRunReview   = "run.review"
	TypeRunDecision = "run.decision"
)

// Agent-mode decision verbs — the FLAT-STEP-MODEL v1 vocabulary (TASK-242). EXACTLY
// these four: no graph reshaping (branch/insert/skip) in v1. The shell rejects any
// verb outside this set (treated as advance-is-not-granted → it does not advance).
//
//   - DecisionAdvance: the step's output is good; proceed to the next step.
//   - DecisionRedo: the output is fundamentally wrong; re-dispatch the SAME step with
//     the agent's Feedback threaded into the worker's next prompt.
//   - DecisionEdit: the agent applied a fix-up edit to the deliverable itself (unbounded
//     per Lena 2026-06-29); then advance. The edit is the agent's own act on the
//     deliverable artifact — NOT a run-envelope write (single-writer holds).
//   - DecisionStop: stop the run now (terminal).
const (
	DecisionAdvance = "advance"
	DecisionRedo    = "redo-with-feedback"
	DecisionEdit    = "edit-then-advance"
	DecisionStop    = "stop"
)

// IsDecisionVerb reports whether v is one of the four FLAT-STEP-MODEL v1 verbs. The
// shell uses it to REJECT any graph-reshaping verb (branch/insert/skip) a confused agent
// might emit: an unknown verb is not "advance", so the run never silently advances on it.
func IsDecisionVerb(v string) bool {
	switch v {
	case DecisionAdvance, DecisionRedo, DecisionEdit, DecisionStop:
		return true
	}
	return false
}

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

	// KindVerify is an independent verification step (D8). It dispatches a SEPARATE
	// worker (a producer cannot verify itself) charged with rigorously verifying the
	// run's deliverable: fetch the prior steps' real artifacts, BUILD the change and
	// RUN the relevant tests, check EACH acceptance criterion as a property with an
	// adversarial/negative case, and report HONESTLY — done only if every AC is met AND
	// the build/tests are green, else outcome=blocked with a verdict artifact enumerating
	// what failed. Placed BEFORE a brief so a run cannot reach done over a failed
	// verification. Additive — existing kinds are unchanged.
	KindVerify = "verify"
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

	// Repo is the absolute path to the git repository this run's work happens in
	// (TASK-256). When set, the coordinator provisions one isolated git worktree per
	// run (a fresh branch sxrun/<id> off RepoRef, or HEAD when RepoRef is empty),
	// runs every step inside it (threaded to the worker via SpawnRequest.Workdir →
	// SEXTANT_PI_WORKDIR), and tears it down when the run goes terminal. It comes from
	// the RUN/TEMPLATE definition, never an operator-set env var — so a request can't
	// point a worker at an arbitrary checkout. Omitted = no provisioning (the worker
	// falls back to the recipe's scratch default; today's behaviour for repo-less runs).
	Repo string `json:"repo,omitempty"`
	// RepoRef is the optional base ref the per-run worktree branches from (a branch,
	// tag, or commit). Omitted = the repo's current HEAD. Ignored when Repo is empty.
	RepoRef string `json:"repo_ref,omitempty"`

	// AgentMode opts this run into the long-lived coordinator-AGENT review loop (TASK-242):
	// at each completed step the programmatic shell consults a resident reviewer agent and
	// applies its run.decision. ADDITIVE and opt-in — absent/false is the existing
	// programmatic path, byte-unchanged (the shell never stands up or consults an agent).
	AgentMode bool `json:"agent_mode,omitempty"`
}

// RunStep is one unit of work. Kind work → dispatch an agent; verify → dispatch a
// SEPARATE agent to independently verify the run's deliverable (build + run tests +
// adversarial AC checks), which blocks the run on a failed verification; checkpoint →
// pause for the operator; brief → write the terminal stopping brief. Agent is filled
// from the spawn.ack for work/verify/brief steps.
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
	// Model is the optional per-step model declaration (TASK-245). When set, the
	// dispatcher runs this step's worker on this model instead of its default. The
	// field flows RunStep → SpawnRequest.Model → dispatcher env SX_AGENT_MODEL so
	// the pi recipe picks it up. Omitted = coordinator/dispatcher default applies.
	Model string `json:"model,omitempty"`
	// TimeoutSecs is the optional per-step timeout, in whole seconds (TASK-257). When
	// > 0 the coordinator bounds this step's dispatch (spawn.ack + the agent's
	// step-done) by it instead of the run-wide --step-timeout default. A coding step
	// (plan, implement, review) routinely runs minutes, far past the 90s default, so a
	// run carries the right budget IN ITS DEFINITION rather than relying on a hand-run
	// coordinator flag. Omitted/0 = the coordinator's flag/default applies. Seconds (an
	// int), not a Go time.Duration string, so the Go run and its TS peer serialize the
	// SAME wire bytes (the field is co-equal across languages).
	TimeoutSecs int `json:"timeout_secs,omitempty"`
}

// Timeout returns the step's declared per-step timeout (TASK-257), or 0 when unset.
// The coordinator falls back to its run-wide default when this is 0. Centralised here
// so the seconds→Duration conversion lives with the field, not at each call site.
func (s RunStep) Timeout() time.Duration {
	if s.TimeoutSecs <= 0 {
		return 0
	}
	return time.Duration(s.TimeoutSecs) * time.Second
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
//
// Artifacts is the TYPED proof channel (TASK-243/244): the refs the worker reports it
// PRODUCED this step, collected mechanically by the worker's runtime on every
// artifact create/update — not free text it self-asserts. It is the ONLY thing the
// deterministic stop-gate decides from. The gate existence-checks each ref and never
// reads/parses the brief's body (AC2/AC4): proof is declared in this typed metadata,
// not in brief prose, so the gate is independent of whatever shape a brief happens to
// use. Whether the brief's prose accurately DESCRIBES the deliverable is content — the
// opt-in agent-mode reviewer's job (TASK-242), never the deterministic gate's.
type RunEvent struct {
	Type      string             `json:"$type"`
	Step      string             `json:"step,omitempty"`
	Status    string             `json:"status"`
	By        string             `json:"by,omitempty"`
	Note      string             `json:"note,omitempty"`
	Outcome   string             `json:"outcome,omitempty"` // brief step: "done" | "blocked"
	Reason    string             `json:"reason,omitempty"`  // when outcome=blocked: a one-line why (verify step, D8)
	Artifacts []ProducedArtifact `json:"artifacts,omitempty"`
}

// RunControl is a cooperative control message on RunControlSubject(id).
type RunControl struct {
	Type string `json:"$type"`
	Verb string `json:"verb"`
}

// RunReview is the agent-mode review REQUEST the programmatic shell publishes on
// RunReviewSubject(id) when a step completes and the run is in agent mode (TASK-242). It
// hands the long-lived coordinator agent everything it needs to judge the step's output:
// the step id, the objective, and the typed refs the step's worker produced (so the agent
// can artifact_get each and READ its content — the one sanctioned content read). The shell
// then awaits the agent's RunDecision on RunDecisionSubject(id). The shell decides nothing
// from content here; it only relays metadata and the produced refs.
type RunReview struct {
	Type      string             `json:"$type"`
	Step      string             `json:"step"`
	Objective string             `json:"objective,omitempty"`
	Label     string             `json:"label,omitempty"`
	Produced  []ProducedArtifact `json:"produced,omitempty"`
}

// RunDecision is the agent's reply the shell applies (TASK-242). Verb is one of the four
// FLAT-STEP-MODEL v1 verbs (IsDecisionVerb); Feedback is threaded into the re-dispatch
// prompt on redo-with-feedback; Reason is recorded on the run's activity trail (so the
// decision is observable — AC#6 "no silent edit bypassing the decision/activity trail").
// The agent emits this as a plain msg.* record — it has NO write path to the run envelope
// (single-writer = the shell), so applying it never makes the agent a second writer.
type RunDecision struct {
	Type     string `json:"$type"`
	Step     string `json:"step"`
	Verb     string `json:"verb"`
	Feedback string `json:"feedback,omitempty"`
	Reason   string `json:"reason,omitempty"`
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

func (r RunReview) Marshal() json.RawMessage {
	r.Type = TypeRunReview
	b, _ := json.Marshal(r)
	return b
}

func ParseRunReview(record json.RawMessage) (RunReview, bool) {
	var r RunReview
	if err := json.Unmarshal(record, &r); err != nil || r.Type != TypeRunReview {
		return RunReview{}, false
	}
	return r, true
}

func (d RunDecision) Marshal() json.RawMessage {
	d.Type = TypeRunDecision
	b, _ := json.Marshal(d)
	return b
}

func ParseRunDecision(record json.RawMessage) (RunDecision, bool) {
	var d RunDecision
	if err := json.Unmarshal(record, &d); err != nil || d.Type != TypeRunDecision {
		return RunDecision{}, false
	}
	return d, true
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

// Agent-mode review subjects (TASK-242). The shell publishes a run.review on .review and
// awaits the agent's run.decision on .decision. Distinct machine channels, parallel to
// .events (worker→shell step-done) and .control (operator approve/cancel): the shell is
// the SOLE writer of the run envelope on all of them — the agent only emits a decision.
func RunReviewSubject(id string) string   { return "msg.workflow.run." + id + ".review" }
func RunDecisionSubject(id string) string { return "msg.workflow.run." + id + ".decision" }

// RunTopicSubject is the run's OPERATOR thread: msg.topic.run.<id>. This is the
// human-facing channel the dash run view posts to — distinct from the machine
// channels (.events is the worker→coordinator step-done signal; .control is the
// cooperative approve/cancel lexicon). An operator chat.message here STEERS the run:
// the coordinator subscribes it and routes the steer to the active step's worker
// (TASK-246). The dash's run-topic composer publishes here (workengine.jsx RUN_TOPIC).
func RunTopicSubject(id string) string { return "msg.topic.run." + id }

// TypeChatMessage is the opaque chat convention an operator post uses ({$type,text}).
// It is NOT a run-record type — the run topic carries plain chat, not a new wire
// contract — so the coordinator parses it structurally (ParseChatSteer) rather than
// minting a steer record. Kept here so the steer path and its proof reference one name.
const TypeChatMessage = "chat.message"

// ParseChatSteer reads an operator steer off the run topic: a chat.message with
// non-empty text. It returns the text and ok=false for anything else (a non-chat
// record, or a chat with empty text), so the coordinator acts only on a real steer.
// The steer rides this plain chat.message — no run-envelope write by the operator —
// which keeps the coordinator the single writer of the run state (ADR-0048).
func ParseChatSteer(record json.RawMessage) (string, bool) {
	var m struct {
		Type string `json:"$type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(record, &m); err != nil || m.Type != TypeChatMessage || m.Text == "" {
		return "", false
	}
	return m.Text, true
}

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

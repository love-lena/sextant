package violet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/love-lena/sextant/protocol/wire"
)

// Mobilizer is violet's ACTION SURFACE: how she MOBILIZES work without making
// the operator's decisions (Lena's scope addition, 2026-06). The whole point of
// the design is that the surface can only START work — it has no method to
// merge, approve, write a verdict, or write a foreign artifact. Starting work is
// categorically not deciding; that boundary is enforced by the type, not by
// convention. (signal-not-manage holds for decisions; mobilizing is the new,
// bounded power.)
//
// SECURITY (TASK-158): every method here starts work under a FRESH SCOPED
// identity, never violet's own ambient creds and never the principal's. Spawns
// go through the M5.2 dispatcher (cmd/sextant-dispatch), which mints a new
// kind=agent credential per child — so violet hands out no creds at all; she
// only publishes a DATA request and the dispatcher mints the scoped identity.
// Workflow runs are started the same way: a request the workflow coordinator
// consumes, never an in-process engine and never violet's creds handed off.
type Mobilizer interface {
	// SpawnAgent asks the dispatcher to stand up a fresh, scoped agent for a
	// task (e.g. gather requirements, run a step). It publishes a spawn.request
	// (DATA only — a prompt + lineage labels, never a command); the dispatcher
	// mints the child's own identity. Returns the request frame id for
	// correlation with the dispatcher's spawn.ack.
	SpawnAgent(ctx context.Context, req SpawnSpec) (requestID string, err error)

	// StartWorkflow asks the workflow coordinator to begin a declarative run
	// (plan → steps → PR), which itself spawns scoped step-agents via the
	// dispatcher. v1 publishes the start request; the coordinator (a separate
	// long-lived client under its own creds) owns the run.
	StartWorkflow(ctx context.Context, req WorkflowSpec) (requestID string, err error)
}

// SpawnSpec is a request to mobilize one fresh agent. Prompt is the task (DATA,
// not a command); Nickname/Job are lineage labels.
type SpawnSpec struct {
	Prompt   string
	Nickname string
	Job      string
}

// WorkflowSpec is a request to start a workflow run. PlanRef names a plan the
// coordinator can load (an artifact name or a workflow id); Note is free-form
// context. v1 carries the reference and lets the coordinator own plan loading —
// violet does not embed an engine (engine-as-a-library-in-a-client, never in
// violet's core).
type WorkflowSpec struct {
	PlanRef string
	Note    string
}

// busMobilizer is the v1 Mobilizer: it publishes spawn/workflow-start requests
// on the bus under violet's own creds. The dispatcher and coordinator (existing
// reference clients) consume them and mint/own the scoped work — so this surface
// can mobilize a cold start with NO persistent crew, while violet hands out no
// credentials and makes no operator decision.
type busMobilizer struct {
	pub          publisher
	self         string
	spawnSubject string // the dispatcher's spawn-request subject (default msg.topic.spawn)
	wfSubject    string // the workflow coordinator's start subject (default msg.topic.workflow.start)
}

// spawnRequestRecord mirrors cmd/sextant-dispatch's SpawnRequest record shape so
// the existing dispatcher consumes violet's requests unchanged. $type is the
// spawn.request discriminator; Parent is informational (the bus stamps the true
// author).
type spawnRequestRecord struct {
	Type     string `json:"$type"`
	Prompt   string `json:"prompt"`
	Nickname string `json:"nickname,omitempty"`
	Job      string `json:"job,omitempty"`
	Parent   string `json:"parent,omitempty"`
}

// workflowStartRecord is the start request for the workflow coordinator. It is a
// convention record (opaque to the bus) — the coordinator loads the plan and
// owns the run; violet only asks.
type workflowStartRecord struct {
	Type    string `json:"$type"`
	PlanRef string `json:"planRef,omitempty"`
	Note    string `json:"note,omitempty"`
	By      string `json:"by,omitempty"`
}

func (m *busMobilizer) SpawnAgent(ctx context.Context, req SpawnSpec) (string, error) {
	if req.Prompt == "" {
		return "", fmt.Errorf("violet: spawn request needs a prompt (the task)")
	}
	rec := spawnRequestRecord{
		Type:     "spawn.request",
		Prompt:   req.Prompt,
		Nickname: req.Nickname,
		Job:      req.Job,
		Parent:   m.self,
	}
	b, _ := json.Marshal(rec)
	out, err := m.pub.PublishMsg(ctx, m.spawnSubject, b)
	if err != nil {
		return "", fmt.Errorf("violet: publish spawn.request: %w", err)
	}
	return out.ID, nil
}

func (m *busMobilizer) StartWorkflow(ctx context.Context, req WorkflowSpec) (string, error) {
	rec := workflowStartRecord{
		Type:    "workflow.start",
		PlanRef: req.PlanRef,
		Note:    req.Note,
		By:      m.self,
	}
	b, _ := json.Marshal(rec)
	out, err := m.pub.PublishMsg(ctx, m.wfSubject, wire.Lexicon(b))
	if err != nil {
		return "", fmt.Errorf("violet: publish workflow.start: %w", err)
	}
	return out.ID, nil
}

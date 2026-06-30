package workflow

import (
	"context"
	"encoding/json"
)

// Shared building blocks of the workflow convention (ADR-0011, ADR-0048). A
// workflow is a CONVENTION over the two primitives, run by an ordinary coordinator
// client — no engine in core. The run-record contract lives in run.go; the symbols
// here (step statuses, control verbs, the spawn lexicon, the Ops seam) are shared
// by it and reused verbatim.

// Step statuses shared with the run contract (run.go reuses StepRunning/StepDone).
const (
	StepRunning = "running"
	StepDone    = "done"
)

// Control verbs (cooperative — they ask, they don't compel; ADR-0011). Shared by
// the run contract's RunControl.
const (
	CtlPause   = "pause"
	CtlResume  = "resume"
	CtlCancel  = "cancel"
	CtlApprove = "approve"
)

// Ack/request statuses shared by the run.start lexicon (run.go).
const (
	StatusOK    = "ok"
	StatusError = "error"
)

// Ops is the primitive bus surface a publishing verb is written against: a single
// message.publish. A start request is one fire-and-forget publish (the coordinator
// owns the state envelope; a requester only asks it to start), so the seam is the
// smallest it can be — declared where it is consumed, so the SDK's *Client, the
// conformance Recorder, and the dash's publish shim each satisfy it.
type Ops interface {
	// Publish issues a message.publish on subject (must be under msg.) with record.
	Publish(ctx context.Context, subject string, record json.RawMessage) error
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
	// Model is the requested worker model (TASK-245). When set, the dispatcher sets
	// SX_AGENT_MODEL for the pi recipe so the worker runs on this model. Omitted =
	// the dispatcher's own default applies (currently claude-haiku-4-5).
	Model string `json:"model,omitempty"`
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

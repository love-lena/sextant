package spawn

import (
	"context"
	"encoding/json"
	"fmt"
)

// The spawn lexicon ($type discriminators). These are ordinary message records —
// opaque to the bus, carried on a normal msg.* subject — so the spawn protocol
// is pure convention between requesters and dispatchers, with no wire-protocol or
// epoch surface. See protocol/lexicons/spawn.request.json and spawn.ack.json.
const (
	TypeSpawnRequest = "spawn.request"
	TypeSpawnAck     = "spawn.ack"
)

// SpawnRequest is the spawn.request record: a request for a dispatcher to spawn a
// new client. It carries DATA only — never a command to run. The dispatcher
// launches its OWN trusted harness, so a request can never inject code onto the
// dispatcher's host; the only thing it influences is the prompt and the lineage
// labels. The requester is the bus-stamped frame author, which the dispatcher
// trusts as the spawn's true parent (the Parent field is informational).
type SpawnRequest struct {
	Type     string `json:"$type"`
	Prompt   string `json:"prompt"`
	Nickname string `json:"nickname,omitempty"`
	Job      string `json:"job,omitempty"`
	Parent   string `json:"parent,omitempty"`
	// Model is the requested worker model (TASK-245). When set, the dispatcher sets
	// SX_AGENT_MODEL for the pi recipe so the worker runs on this model instead of
	// its default. Omitted = the dispatcher's configured default applies.
	Model string `json:"model,omitempty"`
}

// SpawnAck is the spawn.ack record: a dispatcher's acknowledgement of one
// spawn.request, carrying the new client's bus-minted id and the lineage. A
// failed spawn still acks, with Status "error" and Error set, so the requester
// is never left waiting on silence (fail-loud).
type SpawnAck struct {
	Type      string `json:"$type"`
	ID        string `json:"id,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	RequestID string `json:"requestId"`
	Job       string `json:"job,omitempty"`
	Parent    string `json:"parent,omitempty"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

const (
	StatusOK    = "ok"
	StatusError = "error"
)

// ParseSpawnRequest decodes a frame record as a spawn.request, reporting false
// for any other $type (e.g. the dispatcher's own spawn.ack echoed back) or a
// request missing the required prompt. The dispatcher ignores everything it
// returns false for.
func ParseSpawnRequest(record json.RawMessage) (SpawnRequest, bool) {
	var r SpawnRequest
	if err := json.Unmarshal(record, &r); err != nil {
		return SpawnRequest{}, false
	}
	if r.Type != TypeSpawnRequest || r.Prompt == "" {
		return SpawnRequest{}, false
	}
	return r, true
}

// marshal renders a spawn.ack as a record payload, stamping the $type. It cannot
// fail for these plain string fields, so it returns the bytes directly.
func (a SpawnAck) Marshal() json.RawMessage {
	a.Type = TypeSpawnAck
	b, _ := json.Marshal(a)
	return b
}

// RequestSubject is the well-known subject a dispatcher watches for spawn.request
// records (and publishes spawn.ack to) — the default the dispatcher, violet, and
// the dash all use.
const RequestSubject = "msg.topic.spawn"

// Ops is the primitive bus surface the spawn verb is written against: a single
// message.publish. A spawn.request is one fire-and-forget message, so the seam is
// deliberately the smallest it can be — declared where it is consumed (a
// consumer-defined interface), so the SDK's *Client, the conformance Recorder, and
// the dash's publish shim each satisfy it without importing one another.
type Ops interface {
	// Publish issues a message.publish on subject (must be under msg.) with record.
	Publish(ctx context.Context, subject string, record json.RawMessage) error
}

// SpawnRequestRecord renders a spawn.request as a canonical record payload,
// stamping $type and — via the struct's omitempty tags — emitting only the lineage
// fields that are set (empty nickname/job/parent are dropped). It is the single
// source of the spawn.request wire shape: the publishing verb and a direct caller
// (the dash, which posts the record over its own transport) share it, so both
// languages emit byte-identical bytes. parent is informational and never injected
// here — a dispatcher trusts the bus-stamped frame author as the true parent.
func SpawnRequestRecord(req SpawnRequest) json.RawMessage {
	req.Type = TypeSpawnRequest
	b, _ := json.Marshal(req)
	return b
}

// RequestSpawn publishes a spawn.request on subject — the single bus operation a
// requester issues to ask a dispatcher to spawn a client. This is the
// engine-as-a-library write the dispatcher's requesters (the dash, violet) used to
// hand-roll; the op-transcript conformance vector pins it to exactly one
// message.publish, and the TS peer (conventions/spawn/ts) emits the identical
// record. It defines no new bus operation — it issues the existing message.publish.
func RequestSpawn(ctx context.Context, ops Ops, req SpawnRequest, subject string) error {
	if err := ops.Publish(ctx, subject, SpawnRequestRecord(req)); err != nil {
		return fmt.Errorf("spawn: publish spawn.request on %s: %w", subject, err)
	}
	return nil
}

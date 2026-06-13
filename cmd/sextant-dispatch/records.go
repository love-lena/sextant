package main

import "encoding/json"

// The spawn lexicon ($type discriminators). These are ordinary message records —
// opaque to the bus, carried on a normal msg.* subject — so the spawn protocol
// is pure convention between requesters and dispatchers, with no wire-protocol or
// epoch surface. See protocol/lexicons/spawn.request.json and spawn.ack.json.
const (
	typeSpawnRequest = "spawn.request"
	typeSpawnAck     = "spawn.ack"
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
	statusOK    = "ok"
	statusError = "error"
)

// parseSpawnRequest decodes a frame record as a spawn.request, reporting false
// for any other $type (e.g. the dispatcher's own spawn.ack echoed back) or a
// request missing the required prompt. The dispatcher ignores everything it
// returns false for.
func parseSpawnRequest(record json.RawMessage) (SpawnRequest, bool) {
	var r SpawnRequest
	if err := json.Unmarshal(record, &r); err != nil {
		return SpawnRequest{}, false
	}
	if r.Type != typeSpawnRequest || r.Prompt == "" {
		return SpawnRequest{}, false
	}
	return r, true
}

// marshal renders a spawn.ack as a record payload, stamping the $type. It cannot
// fail for these plain string fields, so it returns the bytes directly.
func (a SpawnAck) marshal() json.RawMessage {
	a.Type = typeSpawnAck
	b, _ := json.Marshal(a)
	return b
}

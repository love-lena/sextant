// Package agentactivity is the Go parse side of the agent.activity feed
// (protocol/lexicons/agent.activity.json, TASK-235): the harness-neutral stream
// an agent publishes on msg.agent.<id>.activity to SIGNAL its raw work — every
// turn, thinking, tool call, and message — so a reader renders a headless worker
// without attaching to its terminal. pi-bus (@sextant/pi-bus) is the first
// producer; this package is the contract a Go client (the run executor, TASK-236)
// imports to parse the stream rather than re-declaring the shape.
//
// The record is UNTRUSTED: the bus-stamped frame author is authoritative, never a
// field here. This package has no verb and touches no bus — it is the record + the
// subject helper, nothing more (so its closure stays trivially within the
// convention bright line, ADR-0041).
package agentactivity

import "encoding/json"

// KindActivity is the record $type discriminator for the agent.activity lexicon.
const KindActivity = "agent.activity"

// Activity kinds. turn_* bracket one agent turn; tool_* bracket one tool
// execution; thinking carries a turn's reasoning text; message carries its reply.
const (
	KindTurnStart = "turn_start"
	KindTurnEnd   = "turn_end"
	KindToolStart = "tool_start"
	KindToolEnd   = "tool_end"
	KindThinking  = "thinking"
	KindMessage   = "message"
)

// Activity is one observed step of an agent's work (agent.activity). Optional
// fields are omitted when empty so the record stays minimal. The string fields
// args/result/text are TRUNCATED previews — the durable detail is the worker's
// own session log, not the bus record.
type Activity struct {
	Type       string `json:"$type"`
	Kind       string `json:"kind"` // turn_start|turn_end|tool_start|tool_end|thinking|message
	TurnIndex  int    `json:"turnIndex,omitempty"`
	Tool       string `json:"tool,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Args       string `json:"args,omitempty"`
	Result     string `json:"result,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
	Text       string `json:"text,omitempty"`
	Updated    string `json:"updated,omitempty"`
}

// Marshal encodes the record, stamping the $type so a caller never has to.
func (a Activity) Marshal() json.RawMessage {
	a.Type = KindActivity
	b, _ := json.Marshal(a)
	return b
}

// ParseActivity decodes an agent.activity record, returning ok=false for malformed
// JSON or a record whose $type is not agent.activity (so a reader on a shared
// subscription ignores other conventions' records without mistaking them for one).
func ParseActivity(record json.RawMessage) (Activity, bool) {
	var a Activity
	if err := json.Unmarshal(record, &a); err != nil || a.Type != KindActivity {
		return Activity{}, false
	}
	return a, true
}

// ActivitySubject is the per-agent activity stream: msg.agent.<id>.activity
// (entity.id.aspect, parallels workflow.EventsSubject's msg.workflow.<id>.events).
func ActivitySubject(id string) string { return "msg.agent." + id + ".activity" }

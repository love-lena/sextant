package sessionlog

import (
	"encoding/json"
	"time"
)

// Event is the sum type emitted by Stream. Concrete impls:
// AssistantMessage, UserMessage, SystemMessage, RawEvent.
//
// All events expose a stable subset of the wrapper metadata fields
// (uuid / parentUuid / isSidechain / timestamp / sessionId) — these
// recur on every assistant/user/system record and are useful for
// every downstream filter (conversation grouping, subagent tree,
// tool-use timeline).
type Event interface {
	// Kind is the top-level "type" field value from the JSONL line —
	// "assistant", "user", "system", or whatever non-empty value the
	// SDK wrote for record types we don't model. RawEvent uses the
	// raw kind directly; the typed events return their canonical
	// label. Empty when the line had no "type" field at all (the
	// SDK never produces this in practice, but defensive coding is
	// cheap).
	Kind() string

	// Common returns the wrapper-level fields shared across record
	// types. Zero-valued when the line had none (e.g., some metadata
	// types like `mode` have no parentUuid).
	Common() CommonFields

	// RawLine is the verbatim JSONL line bytes (without the trailing
	// newline). Raw mode in the CLI dump prints this directly; the
	// typed view modes parse the structured fields instead.
	RawLine() []byte
}

// CommonFields are the wrapper-level metadata recurring across most
// SDK record types. We project them into one struct so callers don't
// have to type-switch to read uuid / timestamp.
type CommonFields struct {
	UUID        string    `json:"uuid,omitempty"`
	ParentUUID  string    `json:"parentUuid,omitempty"`
	IsSidechain bool      `json:"isSidechain,omitempty"`
	Timestamp   time.Time `json:"timestamp,omitempty"`
	SessionID   string    `json:"sessionId,omitempty"`
}

// commonRaw is the JSON shape of the wrapper fields. Decoded once
// per line and converted into the canonical CommonFields above. We
// keep parentUuid as a *string in the raw shape because the SDK
// emits explicit JSON null for top-level (root) records, which
// json.Unmarshal otherwise leaves at the zero value indistinguishable
// from "absent" — a future filter may care about the difference.
type commonRaw struct {
	UUID        string  `json:"uuid,omitempty"`
	ParentUUID  *string `json:"parentUuid,omitempty"`
	IsSidechain bool    `json:"isSidechain,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"`
	SessionID   string  `json:"sessionId,omitempty"`
}

func (c commonRaw) toCommon() CommonFields {
	out := CommonFields{
		UUID:        c.UUID,
		IsSidechain: c.IsSidechain,
		SessionID:   c.SessionID,
	}
	if c.ParentUUID != nil {
		out.ParentUUID = *c.ParentUUID
	}
	if c.Timestamp != "" {
		// SDK timestamps are RFC3339Nano with a trailing Z. parse
		// best-effort; on failure leave the zero time.
		if t, err := time.Parse(time.RFC3339Nano, c.Timestamp); err == nil {
			out.Timestamp = t
		}
	}
	return out
}

// AssistantMessage is the parsed shape of a `"type":"assistant"`
// record. The wrapped SDK message lives under the `message` field on
// the wire; we hoist its fields up to the event surface so callers
// don't have to plumb through nested objects.
type AssistantMessage struct {
	CommonFields

	// Model is the model identifier the SDK used for this turn.
	Model string

	// StopReason is the SDK's stop_reason ("end_turn", "tool_use",
	// "max_tokens", …). Empty for streaming-mid-turn records.
	StopReason string

	// Usage is the per-turn token accounting. Zero-valued for records
	// that didn't carry usage (rare; the SDK attaches usage on the
	// terminal message of each turn).
	Usage Usage

	// ContentBlocks is the parsed list of content blocks. Each block
	// is one of the typed Block impls (TextBlock, ThinkingBlock,
	// ToolUseBlock, ToolResultBlock); use a type-switch.
	ContentBlocks []Block

	// MessageID is the SDK's `message.id` (e.g. "msg_01abc…"). Useful
	// for cross-referencing API console logs.
	MessageID string

	// RequestID is the wrapper-level `requestId` field — the SDK's
	// outer request handle (separate from MessageID).
	RequestID string

	raw []byte
}

// Kind reports "assistant".
func (a AssistantMessage) Kind() string { return KindAssistant }

// Common returns the wrapper fields.
func (a AssistantMessage) Common() CommonFields { return a.CommonFields }

// RawLine returns the original JSONL bytes.
func (a AssistantMessage) RawLine() []byte { return a.raw }

// UserMessage is the parsed shape of a `"type":"user"` record. User
// records carry either a plain string (operator prompt) or a list of
// content blocks (tool_result returns from the SDK's tool-runner).
//
// Text and ContentBlocks are mutually exclusive on the wire: when the
// SDK emits `message.content` as a string, Text is populated and
// ContentBlocks is nil; when it emits an array, ContentBlocks is
// populated and Text is "".
type UserMessage struct {
	CommonFields

	// Text is the plain-string content when message.content was a
	// string. Empty when message.content was an array.
	Text string

	// ContentBlocks is the parsed block array when message.content
	// was an array. Today the SDK puts tool_result blocks here.
	ContentBlocks []Block

	// PromptID is the SDK's `promptId` wrapper field, set on operator
	// prompts. Empty for tool-result records.
	PromptID string

	raw []byte
}

// Kind reports "user".
func (u UserMessage) Kind() string { return KindUser }

// Common returns the wrapper fields.
func (u UserMessage) Common() CommonFields { return u.CommonFields }

// RawLine returns the original JSONL bytes.
func (u UserMessage) RawLine() []byte { return u.raw }

// SystemMessage is the parsed shape of a `"type":"system"` record.
// The SDK uses system records for hook output, local-command output,
// init notes, and other non-conversational diagnostics.
type SystemMessage struct {
	CommonFields

	// Subtype narrows the system record (e.g. "stop_hook_summary",
	// "init"). Empty when the SDK didn't set it.
	Subtype string

	// Level is the SDK-assigned severity ("suggestion", "info",
	// "warn"…). Empty when not set.
	Level string

	raw []byte
}

// Kind reports "system".
func (s SystemMessage) Kind() string { return KindSystem }

// Common returns the wrapper fields.
func (s SystemMessage) Common() CommonFields { return s.CommonFields }

// RawLine returns the original JSONL bytes.
func (s SystemMessage) RawLine() []byte { return s.raw }

// RawEvent is the fallthrough for any record whose `type` we don't
// have a typed shape for (today: mode, queue-operation, ai-title,
// last-prompt, attachment, worktree-state, file-history-snapshot,
// pr-link, permission-mode) AND for lines that failed to parse.
//
// kind carries the raw `type` value when present so view-mode
// filters can still group by type. ParseError is non-nil when the
// line was malformed JSON; in that case kind is "" and RawLine is
// the unparseable bytes.
type RawEvent struct {
	CommonFields

	// kind is the raw `type` value from the line. Capitalized "Kind"
	// is the method; using a lowercase field avoids collision.
	kind string

	// ParseError is non-nil when the line failed json.Unmarshal.
	// RawLine still holds the bytes so the caller can render the
	// broken line verbatim (raw view mode is the floor).
	ParseError error

	raw []byte
}

// Kind reports the raw `type` value, or "" when the line failed to
// parse at all.
func (r RawEvent) Kind() string { return r.kind }

// Common returns the wrapper fields (may be zero-valued for records
// that have none, like `mode`).
func (r RawEvent) Common() CommonFields { return r.CommonFields }

// RawLine returns the original JSONL bytes.
func (r RawEvent) RawLine() []byte { return r.raw }

// Block is the sum type for assistant/user content blocks.
// Concrete impls: TextBlock, ThinkingBlock, ToolUseBlock,
// ToolResultBlock. Unknown block types fall through to RawBlock.
type Block interface {
	// BlockType is the discriminator: "text", "thinking", "tool_use",
	// "tool_result", or the raw type for unknown blocks.
	BlockType() string
}

// TextBlock is a `{"type":"text","text":"..."}` content block.
type TextBlock struct {
	Text string
}

// BlockType reports "text".
func (TextBlock) BlockType() string { return BlockTypeText }

// ThinkingBlock is a `{"type":"thinking","thinking":"...","signature":"..."}`
// extended-thinking block. Signature is opaque; the operator-facing UI
// shows Thinking only.
type ThinkingBlock struct {
	Thinking  string
	Signature string
}

// BlockType reports "thinking".
func (ThinkingBlock) BlockType() string { return BlockTypeThinking }

// ToolUseBlock is a `{"type":"tool_use","id":"toolu_…","name":"Bash",
// "input":{...}}` block — the assistant invoking a tool.
type ToolUseBlock struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// BlockType reports "tool_use".
func (ToolUseBlock) BlockType() string { return BlockTypeToolUse }

// ToolResultBlock is a `{"type":"tool_result","tool_use_id":"toolu_…",
// "content":"…" | [...], "is_error":bool}` block — the runner returning
// a tool's output. Content is either a plain string or an array of
// sub-blocks (rare; e.g. images). When the SDK returns a string, Text
// is set; when it returns an array, Blocks is set.
type ToolResultBlock struct {
	ToolUseID string
	IsError   bool
	Text      string
	Blocks    []Block
}

// BlockType reports "tool_result".
func (ToolResultBlock) BlockType() string { return BlockTypeToolResult }

// RawBlock is the fallthrough for unknown block types. Raw holds the
// verbatim JSON bytes so the operator can still inspect them.
type RawBlock struct {
	TypeName string
	Raw      json.RawMessage
}

// BlockType reports the raw type string.
func (r RawBlock) BlockType() string { return r.TypeName }

// Usage mirrors the SDK's `message.usage` shape — the per-turn token
// accounting. We surface the 5m/1h cache tiers explicitly because the
// Usage view mode renders cache-hit ratios per tier.
//
// Fields kept as int (not int64) because the SDK's counts comfortably
// fit and matching the JSON's plain integer keeps round-trip tests
// painless.
type Usage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`

	// CacheCreation breaks down cache_creation_input_tokens by tier
	// (5-minute vs. 1-hour). Zero values when the SDK didn't emit
	// the per-tier breakdown — older sessions only carry the rollup
	// above.
	CacheCreation CacheCreationTiers `json:"cache_creation"`

	// ServiceTier is the SDK's `service_tier` field
	// ("standard", "priority", …).
	ServiceTier string `json:"service_tier,omitempty"`
}

// CacheCreationTiers is the per-tier breakdown nested under
// `usage.cache_creation` on each assistant record. Tracking the 5m vs
// 1h tiers separately lets the Usage view mode call out whether the
// agent's been hitting the cheap (5m) tier or the expensive (1h)
// long-lived cache.
type CacheCreationTiers struct {
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
}

// Canonical Kind strings. Exported so callers can compare without
// the per-package import-and-stringify pattern.
const (
	KindAssistant = "assistant"
	KindUser      = "user"
	KindSystem    = "system"
)

// Canonical BlockType strings. Mirrors the SDK's content-block
// discriminator values.
const (
	BlockTypeText       = "text"
	BlockTypeThinking   = "thinking"
	BlockTypeToolUse    = "tool_use"
	BlockTypeToolResult = "tool_result"
)

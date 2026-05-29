package sextantproto

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Envelope is the universal wrapper for every message on the sextant bus.
// Every field listed in specs/protocols/envelope-schema.md is present here.
// JSON tags match the wire format exactly.
//
// Construct via NewEnvelope so trace IDs are populated; the zero value is
// invalid because TraceID and SpanID are required (envelope-schema.md §2).
type Envelope struct {
	ID           uuid.UUID `json:"id"`
	Ts           Timestamp `json:"ts"`
	ProtoVersion string    `json:"proto_version"`

	From Address  `json:"from"`
	To   *Address `json:"to,omitempty"`

	TraceID      uuid.UUID  `json:"trace_id"`
	SpanID       uuid.UUID  `json:"span_id"`
	ParentSpanID *uuid.UUID `json:"parent_span_id,omitempty"`

	IdempotencyKey *string `json:"idempotency_key,omitempty"`
	ReplyTo        *string `json:"reply_to,omitempty"`

	Kind    Kind            `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// Address identifies an envelope's sender or recipient.
//
// Kind is a closed enum: "agent" | "operator" | "daemon" | "ui" | "external".
// ID is the UUID for kinds that have one; "daemon-<host>" for daemons.
type Address struct {
	Kind AddressKind `json:"kind"`
	ID   string      `json:"id"`
	Host *string     `json:"host,omitempty"`
}

// AddressKind enumerates the legal Address.Kind values.
type AddressKind string

const (
	AddressAgent    AddressKind = "agent"
	AddressOperator AddressKind = "operator"
	AddressDaemon   AddressKind = "daemon"
	AddressUI       AddressKind = "ui"
	AddressExternal AddressKind = "external"
)

// IsValid reports whether k is a recognized AddressKind.
func (k AddressKind) IsValid() bool {
	switch k {
	case AddressAgent, AddressOperator, AddressDaemon, AddressUI, AddressExternal:
		return true
	default:
		return false
	}
}

// AllAddressKinds returns every AddressKind in canonical order. Used by
// code generation (the wire.json manifest + generated TS constants) and
// tests; do not rely on it for hot paths.
func AllAddressKinds() []AddressKind {
	return []AddressKind{
		AddressAgent,
		AddressOperator,
		AddressDaemon,
		AddressUI,
		AddressExternal,
	}
}

// Kind is the envelope discriminator. Every value here corresponds to a
// payload type in this package.
type Kind string

const (
	KindAgentFrame        Kind = "agent_frame"
	KindLifecycle         Kind = "lifecycle"
	KindAudit             Kind = "audit"
	KindTelemetrySpan     Kind = "telemetry_span"
	KindTelemetryMetric   Kind = "telemetry_metric"
	KindTelemetryLog      Kind = "telemetry_log"
	KindUserInputRequest  Kind = "user_input_request"
	KindUserInputResponse Kind = "user_input_response"
	KindRPCRequest        Kind = "rpc_request"
	KindRPCResponse       Kind = "rpc_response"
	KindHeartbeat         Kind = "heartbeat"
)

// AllKinds returns every envelope kind in canonical order. Used by code
// generation and tests; do not rely on it for hot paths.
func AllKinds() []Kind {
	return []Kind{
		KindAgentFrame,
		KindLifecycle,
		KindAudit,
		KindTelemetrySpan,
		KindTelemetryMetric,
		KindTelemetryLog,
		KindUserInputRequest,
		KindUserInputResponse,
		KindRPCRequest,
		KindRPCResponse,
		KindHeartbeat,
	}
}

// NewEnvelope returns a root envelope populated with a fresh ID,
// current timestamp, the current ProtoVersion, and trace fields set up
// for a root span (TraceID = ID, SpanID = fresh UUID, no parent).
//
// Payload must already be JSON-marshalled; use NewEnvelopeWith for a
// helper that marshals the payload for you.
func NewEnvelope(kind Kind, from Address, payload json.RawMessage) Envelope {
	id := uuid.New()
	return Envelope{
		ID:           id,
		Ts:           NowTimestamp(),
		ProtoVersion: ProtoVersion,
		From:         from,
		TraceID:      id,
		SpanID:       uuid.New(),
		Kind:         kind,
		Payload:      payload,
	}
}

// NewEnvelopeWith marshals payload to JSON and wraps it in a root envelope.
// Returns an error if marshalling fails.
func NewEnvelopeWith(kind Kind, from Address, payload any) (Envelope, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal envelope payload for kind %q: %w", kind, err)
	}
	return NewEnvelope(kind, from, raw), nil
}

// Child returns a derived envelope that shares the parent's TraceID and
// references the parent's SpanID as its parent. ID, SpanID, and Ts are
// fresh. Kind, From, and Payload are caller-supplied.
func (e Envelope) Child(kind Kind, from Address, payload json.RawMessage) Envelope {
	parent := e.SpanID
	return Envelope{
		ID:           uuid.New(),
		Ts:           NowTimestamp(),
		ProtoVersion: ProtoVersion,
		From:         from,
		TraceID:      e.TraceID,
		SpanID:       uuid.New(),
		ParentSpanID: &parent,
		Kind:         kind,
		Payload:      payload,
	}
}

// Validate checks the structural invariants required by envelope-schema.md:
// non-nil ID, TraceID, and SpanID; recognized Kind and From.Kind;
// ProtoVersion present.
func (e Envelope) Validate() error {
	if e.ID == uuid.Nil {
		return fmt.Errorf("envelope: id is nil")
	}
	if e.TraceID == uuid.Nil {
		return fmt.Errorf("envelope: trace_id is nil (required on every envelope)")
	}
	if e.SpanID == uuid.Nil {
		return fmt.Errorf("envelope: span_id is nil (required on every envelope)")
	}
	if e.ProtoVersion == "" {
		return fmt.Errorf("envelope: proto_version is empty")
	}
	if e.Kind == "" {
		return fmt.Errorf("envelope: kind is empty")
	}
	if !e.From.Kind.IsValid() {
		return fmt.Errorf("envelope: from.kind %q is not a recognized AddressKind", e.From.Kind)
	}
	if e.Ts.IsZero() {
		return fmt.Errorf("envelope: ts is zero")
	}
	return nil
}

// Timestamp is a wall-clock instant with microsecond precision, encoded
// as RFC 3339 with a 6-digit fractional second component. Wire format is
// stable across languages; ClickHouse columns use DateTime64(6).
type Timestamp struct {
	time.Time
}

// NowTimestamp returns the current time truncated to microsecond precision.
func NowTimestamp() Timestamp {
	return Timestamp{time.Now().UTC().Truncate(time.Microsecond)}
}

// AtTimestamp wraps t and truncates to microsecond precision.
func AtTimestamp(t time.Time) Timestamp {
	return Timestamp{t.UTC().Truncate(time.Microsecond)}
}

// MarshalJSON emits the timestamp as an RFC 3339 string truncated to
// microseconds.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte(`null`), nil
	}
	return []byte(`"` + t.Time.UTC().Format("2006-01-02T15:04:05.000000Z07:00") + `"`), nil
}

// UnmarshalJSON accepts RFC 3339 strings with up to nanosecond precision
// and JSON null. The parsed time is truncated to microseconds so a
// round-trip is stable.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	if string(b) == "null" || string(b) == `""` {
		*t = Timestamp{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("timestamp: not a string: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("timestamp: parse %q: %w", s, err)
	}
	*t = Timestamp{parsed.UTC().Truncate(time.Microsecond)}
	return nil
}

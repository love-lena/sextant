package sextantproto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewEnvelopeIsRootSpan(t *testing.T) {
	from := Address{Kind: AddressOperator, ID: "lena"}
	env := NewEnvelope(KindAudit, from, json.RawMessage(`{}`))

	if env.ID == uuid.Nil {
		t.Fatal("id should be set")
	}
	if env.TraceID != env.ID {
		t.Fatalf("root TraceID must equal ID; got %s vs %s", env.TraceID, env.ID)
	}
	if env.SpanID == uuid.Nil {
		t.Fatal("SpanID must be set")
	}
	if env.ParentSpanID != nil {
		t.Fatal("root envelope must have no ParentSpanID")
	}
	if env.ProtoVersion != ProtoVersion {
		t.Fatalf("proto_version = %q, want %q", env.ProtoVersion, ProtoVersion)
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestChildPropagatesTrace(t *testing.T) {
	from := Address{Kind: AddressAgent, ID: uuid.NewString()}
	root := NewEnvelope(KindAgentFrame, from, json.RawMessage(`{}`))
	child := root.Child(KindAgentFrame, from, json.RawMessage(`{}`))

	if child.TraceID != root.TraceID {
		t.Fatalf("child TraceID = %s, want %s", child.TraceID, root.TraceID)
	}
	if child.ParentSpanID == nil || *child.ParentSpanID != root.SpanID {
		t.Fatalf("child ParentSpanID should point at root SpanID")
	}
	if child.SpanID == root.SpanID {
		t.Fatal("child SpanID must differ from root")
	}
	if child.ID == root.ID {
		t.Fatal("child ID must differ from root")
	}
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	host := "host-a"
	idem := "idem-key-123"
	reply := "_INBOX.replyto"
	parent := uuid.New()
	from := Address{Kind: AddressDaemon, ID: "daemon-host-a", Host: &host}
	env := Envelope{
		ID:             uuid.New(),
		Ts:             AtTimestamp(time.Date(2026, 5, 24, 10, 30, 45, 123456*1000, time.UTC)),
		ProtoVersion:   ProtoVersion,
		From:           from,
		To:             &Address{Kind: AddressUI, ID: "ui-1"},
		TraceID:        uuid.New(),
		SpanID:         uuid.New(),
		ParentSpanID:   &parent,
		IdempotencyKey: &idem,
		ReplyTo:        &reply,
		Kind:           KindRPCRequest,
		Payload:        json.RawMessage(`{"verb":"list_agents","args":{}}`),
	}

	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if back.ID != env.ID {
		t.Fatalf("id roundtrip; got %s, want %s", back.ID, env.ID)
	}
	if back.Ts.UTC() != env.Ts.UTC() {
		t.Fatalf("ts roundtrip; got %v, want %v", back.Ts, env.Ts)
	}
	if back.From.Kind != env.From.Kind || back.From.ID != env.From.ID {
		t.Fatalf("from roundtrip; got %+v, want %+v", back.From, env.From)
	}
	if back.From.Host == nil || env.From.Host == nil || *back.From.Host != *env.From.Host {
		t.Fatalf("from host roundtrip; got %v, want %v", back.From.Host, env.From.Host)
	}
	if back.To == nil || back.To.Kind != env.To.Kind || back.To.ID != env.To.ID {
		t.Fatalf("to roundtrip; got %+v, want %+v", back.To, env.To)
	}
	if back.TraceID != env.TraceID || back.SpanID != env.SpanID {
		t.Fatalf("trace/span roundtrip mismatch")
	}
	if back.ParentSpanID == nil || *back.ParentSpanID != parent {
		t.Fatalf("parent span roundtrip mismatch")
	}
	if back.IdempotencyKey == nil || *back.IdempotencyKey != idem {
		t.Fatalf("idempotency key roundtrip mismatch")
	}
	if back.ReplyTo == nil || *back.ReplyTo != reply {
		t.Fatalf("reply_to roundtrip mismatch")
	}
	if back.Kind != KindRPCRequest {
		t.Fatalf("kind roundtrip mismatch")
	}
	if string(back.Payload) != string(env.Payload) {
		t.Fatalf("payload roundtrip; got %s, want %s", back.Payload, env.Payload)
	}
}

func TestEnvelopeValidateFlagsMissingFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(e *Envelope)
		wantSub string
	}{
		{"nil id", func(e *Envelope) { e.ID = uuid.Nil }, "id is nil"},
		{"nil trace_id", func(e *Envelope) { e.TraceID = uuid.Nil }, "trace_id is nil"},
		{"nil span_id", func(e *Envelope) { e.SpanID = uuid.Nil }, "span_id is nil"},
		{"empty kind", func(e *Envelope) { e.Kind = "" }, "kind is empty"},
		{"empty proto", func(e *Envelope) { e.ProtoVersion = "" }, "proto_version is empty"},
		{"bad from.kind", func(e *Envelope) { e.From.Kind = "robot" }, "not a recognized AddressKind"},
		{"zero ts", func(e *Envelope) { e.Ts = Timestamp{} }, "ts is zero"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := NewEnvelope(KindAudit, Address{Kind: AddressOperator, ID: "lena"}, json.RawMessage(`{}`))
			tc.mutate(&env)
			err := env.Validate()
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !containsSubstr(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestNewEnvelopeWithMarshalsPayload(t *testing.T) {
	type body struct {
		Verb string `json:"verb"`
	}
	from := Address{Kind: AddressAgent, ID: uuid.NewString()}
	env, err := NewEnvelopeWith(KindRPCRequest, from, body{Verb: "list_agents"})
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	if string(env.Payload) != `{"verb":"list_agents"}` {
		t.Fatalf("payload = %s", env.Payload)
	}
}

func TestTimestampPrecisionIsMicroseconds(t *testing.T) {
	withNanos := time.Date(2026, 1, 2, 3, 4, 5, 123_456_789, time.UTC)
	ts := AtTimestamp(withNanos)
	if ts.Nanosecond()%1_000 != 0 {
		t.Fatalf("ts nanos = %d; should be a multiple of 1000 (μs truncation)", ts.Nanosecond())
	}

	raw, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `"2026-01-02T03:04:05.123456Z"` {
		t.Fatalf("ts wire = %s", raw)
	}

	var back Timestamp
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !back.Equal(ts.Time) {
		t.Fatalf("ts roundtrip mismatch; got %v, want %v", back, ts)
	}
}

func TestTimestampHandlesNullAndEmpty(t *testing.T) {
	for _, in := range []string{`null`, `""`} {
		var ts Timestamp
		if err := json.Unmarshal([]byte(in), &ts); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		if !ts.IsZero() {
			t.Fatalf("input %s should produce zero timestamp", in)
		}
	}
}

func TestAddressKindIsValid(t *testing.T) {
	for _, k := range []AddressKind{AddressAgent, AddressOperator, AddressDaemon, AddressUI, AddressExternal} {
		if !k.IsValid() {
			t.Fatalf("%q should be valid", k)
		}
	}
	for _, k := range []AddressKind{"", "robot", "human"} {
		if k.IsValid() {
			t.Fatalf("%q should not be valid", k)
		}
	}
}

func containsSubstr(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

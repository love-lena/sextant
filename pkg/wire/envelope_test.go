package wire

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEnvelopeWireFieldNames(t *testing.T) {
	b, err := Encode(New("agent-1", json.RawMessage(`{"x":1}`)))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, k := range []string{"id", "sender", "kind", "epoch", "record"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing wire field %q", k)
		}
	}
	if len(m) != 5 {
		t.Errorf("unexpected field count %d (want 5): %v", len(m), m)
	}
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	in := Envelope{
		ID:     "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Sender: "coordinator-7",
		Kind:   KindMessage,
		Epoch:  Epoch,
		Record: json.RawMessage(`{"$type":"app.sextant.review.request","artifact":"artifact/plan/abc"}`),
	}
	b, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.ID != in.ID || out.Sender != in.Sender || out.Kind != in.Kind || out.Epoch != in.Epoch {
		t.Errorf("core mismatch:\n got  %+v\n want %+v", out, in)
	}
	if !jsonEqual(t, in.Record, out.Record) {
		t.Errorf("record not preserved:\n got  %s\n want %s", out.Record, in.Record)
	}
}

func TestNewProducesValidEnvelope(t *testing.T) {
	e := New("agent-1", json.RawMessage(`{"x":1}`))
	if e.Kind != KindMessage {
		t.Errorf("kind = %q, want %q", e.Kind, KindMessage)
	}
	if e.Epoch != Epoch {
		t.Errorf("epoch = %d, want %d", e.Epoch, Epoch)
	}
	if _, err := ULIDTimestamp(e.ID); err != nil {
		t.Errorf("New id is not a valid ULID: %v", err)
	}
	if err := e.Validate(); err != nil {
		t.Errorf("New envelope failed Validate: %v", err)
	}
}

// jsonEqual reports whether two JSON byte slices are semantically equal.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	return reflect.DeepEqual(x, y)
}

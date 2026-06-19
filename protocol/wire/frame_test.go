package wire

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFrameWireFieldNames(t *testing.T) {
	b, err := Encode(New("client-1", json.RawMessage(`{"x":1}`)))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	// A message frame carries the wrapper core; the artifact-only fields
	// (revision/createdAt/updatedAt) are omitempty and absent here.
	for _, k := range []string{"id", "author", "kind", "epoch", "record"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing wire field %q", k)
		}
	}
	if len(m) != 5 {
		t.Errorf("unexpected field count %d (want 5): %v", len(m), m)
	}
}

func TestFrameJSONRoundTrip(t *testing.T) {
	in := Frame{
		ID:     "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Author: "coordinator-7",
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
	if out.ID != in.ID || out.Author != in.Author || out.Kind != in.Kind || out.Epoch != in.Epoch {
		t.Errorf("core mismatch:\n got  %+v\n want %+v", out, in)
	}
	if !jsonEqual(t, in.Record, out.Record) {
		t.Errorf("record not preserved:\n got  %s\n want %s", out.Record, in.Record)
	}
}

func TestArtifactFrameRoundTrip(t *testing.T) {
	in := Frame{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Author:    "coordinator-7",
		Kind:      KindArtifact,
		Epoch:     Epoch,
		Record:    json.RawMessage(`{"$type":"app.sextant.plan","title":"the plan"}`),
		Revision:  3,
		CreatedAt: "2026-06-04T22:00:00Z",
		UpdatedAt: "2026-06-04T22:30:00Z",
	}
	b, err := Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	for _, k := range []string{"revision", "createdAt", "updatedAt"} {
		if _, ok := m[k]; !ok {
			t.Errorf("artifact frame missing field %q", k)
		}
	}
	out, err := Decode(b)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Revision != in.Revision || out.CreatedAt != in.CreatedAt || out.UpdatedAt != in.UpdatedAt || out.Kind != KindArtifact {
		t.Errorf("artifact fields not preserved:\n got  %+v\n want %+v", out, in)
	}
	if err := out.Validate(); err != nil {
		t.Errorf("artifact frame failed Validate: %v", err)
	}
}

func TestNewProducesValidMessageFrame(t *testing.T) {
	f := New("client-1", json.RawMessage(`{"x":1}`))
	if f.Kind != KindMessage {
		t.Errorf("kind = %q, want %q", f.Kind, KindMessage)
	}
	if f.Epoch != Epoch {
		t.Errorf("epoch = %d, want %d", f.Epoch, Epoch)
	}
	if _, err := ULIDTimestamp(f.ID); err != nil {
		t.Errorf("New id is not a valid ULID: %v", err)
	}
	if err := f.Validate(); err != nil {
		t.Errorf("New frame failed Validate: %v", err)
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

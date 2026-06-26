package spawn_test

import (
	"context"
	"encoding/json"
	"testing"

	spawn "github.com/love-lena/sextant/conventions/spawn/go"
)

// capturePublisher is a one-method Ops that captures the published operation, so
// the test can assert RequestSpawn emits EXACTLY one message.publish (a publish-only
// verb — no get/update; it defines no new bus operation).
type capturePublisher struct {
	calls []published
}

type published struct {
	subject string
	record  json.RawMessage
}

func (p *capturePublisher) Publish(_ context.Context, subject string, record json.RawMessage) error {
	p.calls = append(p.calls, published{subject: subject, record: record})
	return nil
}

func TestRequestSpawnEmitsExactlyOnePublish(t *testing.T) {
	p := &capturePublisher{}
	err := spawn.RequestSpawn(context.Background(), p, spawn.SpawnRequest{Prompt: "say hello", Nickname: "alpha"}, spawn.RequestSubject)
	if err != nil {
		t.Fatalf("RequestSpawn: %v", err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("RequestSpawn emitted %d operations, want exactly 1 message.publish", len(p.calls))
	}
	if p.calls[0].subject != spawn.RequestSubject {
		t.Errorf("subject = %q, want %q", p.calls[0].subject, spawn.RequestSubject)
	}
	got, ok := spawn.ParseSpawnRequest(p.calls[0].record)
	if !ok {
		t.Fatalf("published record is not a valid spawn.request: %s", p.calls[0].record)
	}
	if got.Prompt != "say hello" || got.Nickname != "alpha" {
		t.Errorf("published request = %+v", got)
	}
}

// TestSpawnRequestRecordOmitsEmptyLineage pins byte-parity with the TS peer: the
// optional lineage fields are dropped when empty, never emitted as "" (the same
// shape spawnRequestRecord must produce in TS).
func TestSpawnRequestRecordOmitsEmptyLineage(t *testing.T) {
	rec := spawn.SpawnRequestRecord(spawn.SpawnRequest{Prompt: "x", Nickname: "alpha"})
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(rec, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["$type"]; !ok {
		t.Error("record missing $type")
	}
	for _, absent := range []string{"job", "parent"} {
		if _, present := m[absent]; present {
			t.Errorf("empty %q should be omitted, got %s", absent, m[absent])
		}
	}
}

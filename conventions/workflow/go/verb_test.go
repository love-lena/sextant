package workflow_test

import (
	"context"
	"encoding/json"
	"testing"

	workflow "github.com/love-lena/sextant/conventions/workflow/go"
)

// capturePublisher is a one-method Ops that captures the published operation, so
// the test can assert RequestWorkflowStart emits EXACTLY one message.publish (a
// publish-only verb — it defines no new bus operation).
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

func TestRequestWorkflowStartEmitsExactlyOnePublish(t *testing.T) {
	p := &capturePublisher{}
	err := workflow.RequestWorkflowStart(context.Background(), p, workflow.WorkflowStartRequest{
		Prompt: "review and merge", Nonce: "n1", Nickname: "reviewer",
	})
	if err != nil {
		t.Fatalf("RequestWorkflowStart: %v", err)
	}
	if len(p.calls) != 1 {
		t.Fatalf("emitted %d operations, want exactly 1 message.publish", len(p.calls))
	}
	if p.calls[0].subject != workflow.StartSubject {
		t.Errorf("subject = %q, want %q", p.calls[0].subject, workflow.StartSubject)
	}
	got, ok := workflow.ParseWorkflowStartRequest(p.calls[0].record)
	if !ok {
		t.Fatalf("published record is not a valid workflow.start: %s", p.calls[0].record)
	}
	if got.Prompt != "review and merge" || got.Nonce != "n1" || got.Nickname != "reviewer" {
		t.Errorf("published request = %+v", got)
	}
}

// TestWorkflowStartRecordOmitsEmptyOptionals pins byte-parity with the TS peer: the
// optional fields are dropped when empty, never emitted as "".
func TestWorkflowStartRecordOmitsEmptyOptionals(t *testing.T) {
	rec := workflow.WorkflowStartRecord(workflow.WorkflowStartRequest{Prompt: "x"})
	m := map[string]json.RawMessage{}
	if err := json.Unmarshal(rec, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["$type"]; !ok {
		t.Error("record missing $type")
	}
	for _, absent := range []string{"nonce", "nickname", "target", "by"} {
		if _, present := m[absent]; present {
			t.Errorf("empty %q should be omitted, got %s", absent, m[absent])
		}
	}
}

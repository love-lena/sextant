package workflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	workflow "github.com/love-lena/sextant/conventions/workflow/go"
	conf "github.com/love-lena/sextant/sdk/conformance"
)

// The workflow run/v1 conformance suite (TASK-247). It plugs the REAL run/v1 publish
// verbs into the TASK-183 seam and ReplayVectors over the language-neutral vectors
// under protocol/conformance/vectors/workflow — the SAME JSON the TS conv-workflow
// suite replays (FORMAT.md, ADR-0041), so the two are co-equal. The contract's three
// outbound record shapes (run.start / run.event / run.control) are each one
// fire-and-forget message.publish, so each verb's transcript is a single publish — no
// seed. Together they pin the run/v1 wire shapes across Go and TS, which the legacy
// requestWorkflowStart vector used to do for the retired path (TASK-234, PR #284).
//
// The run/v1 input vectors carry a runId alongside the record fields: run.event and
// run.control ride a run-scoped subject (msg.workflow.run.<id>.events|.control), so the
// id is the verb's argument, not part of the published record. The vector pins both
// the subject the id derives and the record bytes.

func vectorsDir() string {
	return filepath.Join("..", "..", "..", "protocol", "conformance", "vectors")
}

// requestRunStartVerb adapts workflow.RequestRunStart to a conformance.Verb: decode the
// vector's input as a run.start request and publish it on RunStartSubject.
func requestRunStartVerb(ctx context.Context, ops conf.Ops, input json.RawMessage) error {
	var req workflow.RunStartRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return fmt.Errorf("workflow conformance: decode requestRunStart input: %w", err)
	}
	return workflow.RequestRunStart(ctx, ops, req)
}

// runEventInput is the emitRunEvent vector's input: the run id (which names the events
// subject) plus the run.event record fields the agent reports.
type runEventInput struct {
	RunID string            `json:"runId"`
	Event workflow.RunEvent `json:"event"`
}

// emitRunEventVerb adapts workflow.EmitRunEvent: decode the run id + event and publish
// the event on the run's events subject.
func emitRunEventVerb(ctx context.Context, ops conf.Ops, input json.RawMessage) error {
	var in runEventInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("workflow conformance: decode emitRunEvent input: %w", err)
	}
	return workflow.EmitRunEvent(ctx, ops, in.RunID, in.Event)
}

// runControlInput is the requestRunControl vector's input: the run id (which names the
// control subject) plus the run.control verb.
type runControlInput struct {
	RunID   string              `json:"runId"`
	Control workflow.RunControl `json:"control"`
}

// requestRunControlVerb adapts workflow.RequestRunControl: decode the run id + control
// and publish it on the run's control subject.
func requestRunControlVerb(ctx context.Context, ops conf.Ops, input json.RawMessage) error {
	var in runControlInput
	if err := json.Unmarshal(input, &in); err != nil {
		return fmt.Errorf("workflow conformance: decode requestRunControl input: %w", err)
	}
	return workflow.RequestRunControl(ctx, ops, in.RunID, in.Control)
}

func workflowRegistry() *conf.Registry {
	reg := conf.NewRegistry()
	reg.Register("workflow", "requestRunStart", requestRunStartVerb)
	reg.Register("workflow", "emitRunEvent", emitRunEventVerb)
	reg.Register("workflow", "requestRunControl", requestRunControlVerb)
	return reg
}

// TestWorkflowConformance replays the run/v1 vectors against the real verbs. With
// -update it (re)records the on-disk sample vectors from the verbs.
func TestWorkflowConformance(t *testing.T) {
	dir := vectorsDir()
	reg := workflowRegistry()

	if conf.Updating() {
		recordWorkflowVectors(t, dir)
	}
	conf.ReplayVectors(t, dir, reg)
}

// recordWorkflowVectors writes the sample run/v1 vectors from the registered verbs (the
// -update path). The transcript exercises the lifecycle a dash/agent drives over the
// contract: request the coordinator start a run, an agent signals a step done, the
// operator approves a checkpoint.
func recordWorkflowVectors(t *testing.T, dir string) {
	t.Helper()

	// run.start: the dash asks the coordinator to adopt a run it just wrote. The input
	// is the domain request minus the $type discriminant — the verb stamps it.
	startDesc := "workflow run/v1: request the coordinator to adopt a run — a single " +
		"message.publish of a run.start on msg.topic.run.start. The dash's nonce is echoed " +
		"in the coordinator's ack; the empty optionals are omitted."
	startInput := mustMarshal(t, struct {
		ID    string `json:"id"`
		Nonce string `json:"nonce"`
	}{ID: "01HRUN", Nonce: "01NONCE"})
	writeWorkflowVector(t, dir, "requestRunStart", startDesc, startInput, requestRunStartVerb)

	// run.event: a dispatched agent signals a step done on the run's events subject.
	eventDesc := "workflow run/v1: a dispatched agent signals step progress — a single " +
		"message.publish of a run.event on msg.workflow.run.<id>.events. The run id names " +
		"the subject; empty optionals are omitted."
	eventInput := mustMarshal(t, struct {
		RunID string `json:"runId"`
		Event struct {
			Step      string                      `json:"step"`
			Status    string                      `json:"status"`
			By        string                      `json:"by"`
			Outcome   string                      `json:"outcome"`
			Artifacts []workflow.ProducedArtifact `json:"artifacts"`
		} `json:"event"`
	}{
		RunID: "01HRUN",
		Event: struct {
			Step      string                      `json:"step"`
			Status    string                      `json:"status"`
			By        string                      `json:"by"`
			Outcome   string                      `json:"outcome"`
			Artifacts []workflow.ProducedArtifact `json:"artifacts"`
		}{
			Step:      "s1",
			Status:    workflow.StepDone,
			By:        "01AGENT",
			Outcome:   "done",
			Artifacts: []workflow.ProducedArtifact{{Name: "findings", Kind: "brief", Status: "review"}},
		},
	})
	writeWorkflowVector(t, dir, "emitRunEvent", eventDesc, eventInput, emitRunEventVerb)

	// run.control: the operator approves a checkpoint on the run's control subject.
	ctlDesc := "workflow run/v1: the operator cooperatively controls a run — a single " +
		"message.publish of a run.control on msg.workflow.run.<id>.control. The run id names " +
		"the subject."
	ctlInput := mustMarshal(t, struct {
		RunID   string `json:"runId"`
		Control struct {
			Verb string `json:"verb"`
		} `json:"control"`
	}{
		RunID: "01HRUN",
		Control: struct {
			Verb string `json:"verb"`
		}{Verb: workflow.CtlApprove},
	})
	writeWorkflowVector(t, dir, "requestRunControl", ctlDesc, ctlInput, requestRunControlVerb)
}

func writeWorkflowVector(t *testing.T, dir, verb, description string, input json.RawMessage, fn conf.Verb) {
	t.Helper()
	v, err := conf.RecordVector(1, "workflow", verb, description, input, fn, nil)
	if err != nil {
		t.Fatalf("record %s: %v", verb, err)
	}
	path := filepath.Join(dir, "workflow", verb+".json")
	if err := conf.WriteVector(path, v); err != nil {
		t.Fatalf("write %s: %v", verb, err)
	}
	t.Logf("re-recorded %s", path)
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal sample input: %v", err)
	}
	return b
}

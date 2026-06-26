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

// The workflow convention's conformance suite. It plugs the REAL RequestWorkflowStart
// verb into the TASK-183 seam and ReplayVectors over the language-neutral vectors
// under protocol/conformance/vectors/workflow — the SAME JSON the TS conv-workflow
// suite replays (FORMAT.md, ADR-0041), so the two are co-equal (TASK-239 AC#7/AC#9).
// The verb is publish-only, so the transcript is a single message.publish — no seed.

func vectorsDir() string {
	return filepath.Join("..", "..", "..", "protocol", "conformance", "vectors")
}

// requestWorkflowStartVerb adapts workflow.RequestWorkflowStart to a
// conformance.Verb: decode the vector's input as a workflow.start request and
// publish it (conf.Ops's method set is a superset of workflow.Ops, so the recorder
// is the verb's Ops unchanged).
func requestWorkflowStartVerb(ctx context.Context, ops conf.Ops, input json.RawMessage) error {
	var req workflow.WorkflowStartRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return fmt.Errorf("workflow conformance: decode requestWorkflowStart input: %w", err)
	}
	return workflow.RequestWorkflowStart(ctx, ops, req)
}

func workflowRegistry() *conf.Registry {
	reg := conf.NewRegistry()
	reg.Register("workflow", "requestWorkflowStart", requestWorkflowStartVerb)
	return reg
}

// TestWorkflowConformance replays the workflow vectors against the real verb. With
// -update it (re)records the on-disk sample vector from the verb.
func TestWorkflowConformance(t *testing.T) {
	dir := vectorsDir()
	reg := workflowRegistry()

	if conf.Updating() {
		recordWorkflowVectors(t, dir)
	}
	conf.ReplayVectors(t, dir, reg)
}

// recordWorkflowVectors writes the sample workflow vector from the registered verb
// (the -update path). The input carries a prompt + nonce + nickname, so the vector
// pins those fields and that the empty optionals (target/by) are omitted.
func recordWorkflowVectors(t *testing.T, dir string) {
	t.Helper()
	const description = "workflow: request the coordinator to start a run — a single " +
		"message.publish of a workflow.start on msg.topic.workflow.start. The dash's " +
		"nonce is echoed in the coordinator's ack; empty optionals (target/by) are omitted."
	input, err := json.Marshal(struct {
		Prompt   string `json:"prompt"`
		Nonce    string `json:"nonce"`
		Nickname string `json:"nickname"`
	}{Prompt: "review and merge the PR", Nonce: "01NONCE", Nickname: "reviewer"})
	if err != nil {
		t.Fatalf("marshal sample input: %v", err)
	}
	v, err := conf.RecordVector(1, "workflow", "requestWorkflowStart", description, input, requestWorkflowStartVerb, nil)
	if err != nil {
		t.Fatalf("record requestWorkflowStart: %v", err)
	}
	path := filepath.Join(dir, "workflow", "requestWorkflowStart.json")
	if err := conf.WriteVector(path, v); err != nil {
		t.Fatalf("write requestWorkflowStart: %v", err)
	}
	t.Logf("re-recorded %s", path)
}

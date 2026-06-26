// Unit tests for the workflow convention records + start verb (the peers of
// conventions/workflow/go's records_test.go + verb_test.go): the state envelope
// round-trips, nextPending skips done steps, isTerminal pins the resume guard, the
// start-record builder omits empty optionals (byte-parity with Go), and
// requestWorkflowStart emits exactly one publish.

import { test } from "node:test";
import assert from "node:assert/strict";
import type { JSONValue } from "@sextant/sdk";
import {
  marshalWorkflow,
  parseWorkflow,
  parseWorkflowEvent,
  nextPending,
  isTerminal,
  workflowStartRecord,
  requestWorkflowStart,
  parseWorkflowStartAck,
  StartSubject,
  KindWorkflow,
  WfRunning,
  WfDone,
  WfPaused,
  WfCancelled,
  WfFailed,
  StepPending,
  StepDone,
  StepRunning,
  StepFailed,
  type Workflow,
  type Ops,
} from "../src/index.js";

function obj(v: JSONValue): { [k: string]: JSONValue } {
  return v as { [k: string]: JSONValue };
}

test("the workflow state envelope round-trips, stamping the versioned $type", () => {
  const wf: Workflow = {
    id: "01WF",
    status: WfRunning,
    owner: "01OWNER",
    steps: [
      { id: "review", kind: "dispatch", nickname: "reviewer", prompt: "review it", status: StepDone, agent: "01AGENT" },
      { id: "merge", kind: "dispatch", status: StepPending },
    ],
  };
  const got = parseWorkflow(marshalWorkflow(wf));
  assert.ok(got, "parseWorkflow returned null for a valid record");
  assert.equal(obj(marshalWorkflow(wf))["$type"], KindWorkflow);
  assert.equal(got!.id, "01WF");
  assert.equal(got!.steps.length, 2);
  assert.equal(got!.steps[0]!.agent, "01AGENT");
  assert.equal(got!.steps[1]!.status, StepPending);
});

test("parseWorkflow rejects a non-workflow record", () => {
  assert.equal(parseWorkflow({ $type: "chat.message", text: "hi" }), null);
  assert.equal(parseWorkflow(null), null);
  assert.equal(parseWorkflow([1, 2]), null);
});

test("nextPending returns the first not-done step", () => {
  const cases: Array<{ steps: Array<{ status: string }>; want: number }> = [
    { steps: [{ status: StepPending }, { status: StepPending }], want: 0 },
    { steps: [{ status: StepDone }, { status: StepPending }], want: 1 },
    { steps: [{ status: StepDone }, { status: StepRunning }], want: 1 },
    { steps: [{ status: StepDone }, { status: StepFailed }], want: 1 },
    { steps: [{ status: StepDone }, { status: StepDone }], want: -1 },
    { steps: [], want: -1 },
  ];
  for (const c of cases) {
    const w: Workflow = { id: "x", status: WfRunning, owner: "o", steps: c.steps.map((s) => ({ id: "s", kind: "k", status: s.status })) };
    assert.equal(nextPending(w), c.want, JSON.stringify(c.steps));
  }
});

test("isTerminal pins the resume guard", () => {
  for (const s of [WfDone, WfCancelled, WfFailed]) assert.equal(isTerminal(s), true, s);
  for (const s of [WfRunning, WfPaused, StepPending, ""]) assert.equal(isTerminal(s), false, s);
});

test("parseWorkflowEvent accepts an event and rejects other records", () => {
  const ev = parseWorkflowEvent({ $type: "workflow.event", step: "review", status: StepDone, by: "01AGENT" });
  assert.ok(ev);
  assert.equal(ev!.step, "review");
  assert.equal(parseWorkflowEvent({ $type: "workflow.control", verb: "pause" }), null);
});

test("workflowStartRecord stamps $type and omits empty optionals", () => {
  const rec = obj(workflowStartRecord({ prompt: "x" }));
  assert.equal(rec["$type"], "workflow.start");
  assert.equal(rec["prompt"], "x");
  for (const absent of ["nonce", "nickname", "target", "by"]) {
    assert.equal(absent in rec, false, `empty ${absent} omitted`);
  }
});

test("requestWorkflowStart emits exactly one publish on StartSubject", async () => {
  const calls: { subject: string; record: JSONValue }[] = [];
  const ops: Ops = {
    async publish(subject, record) {
      calls.push({ subject, record });
    },
  };
  await requestWorkflowStart(ops, { prompt: "review and merge", nonce: "n1", nickname: "reviewer" });
  assert.equal(calls.length, 1);
  assert.equal(calls[0]!.subject, StartSubject);
  assert.equal(obj(calls[0]!.record)["nonce"], "n1");
});

test("parseWorkflowStartAck correlates the coordinator's reply", () => {
  const ack = parseWorkflowStartAck({ $type: "workflow.start.ack", nonce: "n1", requestId: "01REQ", workflowId: "01WF", status: "ok" });
  assert.ok(ack);
  assert.equal(ack!.nonce, "n1");
  assert.equal(ack!.workflowId, "01WF");
  assert.equal(parseWorkflowStartAck({ $type: "workflow.start", prompt: "x" }), null);
});

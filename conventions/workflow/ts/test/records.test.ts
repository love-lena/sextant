// Unit tests for the workflow convention records (the peer of
// conventions/workflow/go's run_test.go): the run state envelope round-trips
// stamping the versioned $type, nextPendingRun skips done steps, isTerminalRun pins
// the resume guard, the run.start record builder stamps its $type, and the
// event/control/start parsers reject foreign records.

import { test } from "node:test";
import assert from "node:assert/strict";
import type { JSONValue } from "@sextant/sdk";
import {
  marshalRun,
  parseRun,
  marshalRunEvent,
  parseRunEvent,
  parseRunControl,
  marshalRunReview,
  parseRunReview,
  marshalRunDecision,
  parseRunDecision,
  runReviewSubject,
  runDecisionSubject,
  isDecisionVerb,
  DecisionAdvance,
  DecisionRedo,
  DecisionEdit,
  DecisionStop,
  nextPendingRun,
  isTerminalRun,
  runStartRecord,
  runStateName,
  runEventsSubject,
  runControlSubject,
  KindRun,
  RunRunning,
  RunDone,
  RunBlocked,
  RunCancelled,
  RunWaiting,
  StepUpcoming,
  StepDone,
  StepRunning,
  KindWork,
  KindBrief,
  KindVerify,
  type Run,
} from "../src/index.js";

function obj(v: JSONValue): { [k: string]: JSONValue } {
  return v as { [k: string]: JSONValue };
}

test("the run state envelope round-trips, stamping the versioned $type", () => {
  const run: Run = {
    id: "01HRUN",
    template: null,
    label: "do the thing",
    objective: "do the whole thing",
    status: RunRunning,
    steps: [
      { id: "s1", label: "investigate", kind: KindWork, status: StepRunning },
      { id: "verify", label: "verify the deliverable", kind: KindVerify, status: StepUpcoming },
      { id: "brief", label: "stopping brief", kind: KindBrief, status: StepUpcoming },
    ],
    relates: [{ goal: "g1", crit: "c1", kind: "toward" }],
    activity: [{ id: "a1", glyph: "•", text: "spawned", src: "01HRUN", at: 123 }],
    artifacts: [],
  };
  const got = parseRun(marshalRun(run));
  assert.ok(got, "parseRun returned null for a valid record");
  assert.equal(obj(marshalRun(run))["$type"], KindRun);
  assert.equal(got!.id, "01HRUN");
  assert.equal(got!.steps.length, 3);
  assert.equal(got!.steps[1]!.kind, KindVerify);
  assert.equal(got!.steps[2]!.kind, KindBrief);
});

test("parseRun rejects the OLD sextant.workflow/v1 type and non-objects", () => {
  assert.equal(parseRun({ $type: "sextant.workflow/v1", id: "x" }), null);
  assert.equal(parseRun({ $type: "chat.message", text: "hi" }), null);
  assert.equal(parseRun(null), null);
  assert.equal(parseRun([1, 2]), null);
});

test("nextPendingRun returns the first not-done step", () => {
  const cases: Array<{ steps: Array<{ status: string }>; want: number }> = [
    { steps: [{ status: StepUpcoming }, { status: StepUpcoming }], want: 0 },
    { steps: [{ status: StepDone }, { status: StepUpcoming }], want: 1 },
    { steps: [{ status: StepDone }, { status: StepRunning }], want: 1 },
    { steps: [{ status: StepDone }, { status: StepDone }], want: -1 },
    { steps: [], want: -1 },
  ];
  for (const c of cases) {
    const r: Run = {
      id: "x",
      template: null,
      status: RunRunning,
      steps: c.steps.map((s, i) => ({ id: "s" + i, kind: KindWork, status: s.status })),
      relates: [],
      activity: [],
      artifacts: [],
    };
    assert.equal(nextPendingRun(r), c.want, JSON.stringify(c.steps));
  }
});

test("isTerminalRun pins the resume guard", () => {
  for (const s of [RunDone, RunBlocked, RunCancelled]) assert.equal(isTerminalRun(s), true, s);
  for (const s of [RunRunning, RunWaiting]) assert.equal(isTerminalRun(s), false, s);
});

test("parseRunEvent accepts an event and rejects other records", () => {
  const ev = parseRunEvent(marshalRunEvent({ step: "s1", status: StepDone, by: "01AGENT", outcome: "done" }));
  assert.ok(ev);
  assert.equal(ev!.step, "s1");
  assert.equal(ev!.outcome, "done");
  assert.equal(parseRunEvent({ $type: "chat.message", text: "hi" }), null);
});

test("parseRunControl accepts a control and rejects other records", () => {
  const ctl = parseRunControl({ $type: "run.control", verb: "approve" });
  assert.ok(ctl);
  assert.equal(ctl!.verb, "approve");
  assert.equal(parseRunControl({ $type: "run.event", status: "done" }), null);
});

test("runStartRecord stamps the run.start $type and the subjects are well-formed", () => {
  const rec = obj(runStartRecord({ id: "01HRUN", nonce: "n1" }));
  assert.equal(rec["$type"], "run.start");
  assert.equal(rec["id"], "01HRUN");
  assert.equal(rec["nonce"], "n1");
  assert.equal(runStateName("01H"), "workflow.run.01H");
  assert.equal(runEventsSubject("01H"), "msg.workflow.run.01H.events");
  assert.equal(runControlSubject("01H"), "msg.workflow.run.01H.control");
});

// Agent-mode lexicon (TASK-242): the run.review request + run.decision reply round-trip
// stamping their $types, the parsers reject foreign records, the four decision verbs are
// recognised + a graph-reshaping verb is rejected, and the review subjects are well-formed.
test("the agent-mode review/decision records round-trip and reject foreign records", () => {
  const review = parseRunReview(
    marshalRunReview({ step: "s1", objective: "obj", produced: [{ name: "a", kind: "work" }] }),
  );
  assert.ok(review);
  assert.equal(obj(marshalRunReview({ step: "s1" }))["$type"], "run.review");
  assert.equal(review!.step, "s1");
  assert.equal(review!.produced![0]!.name, "a");
  assert.equal(parseRunReview({ $type: "run.event", status: "done" }), null);

  const dec = parseRunDecision(
    marshalRunDecision({ step: "s1", verb: DecisionRedo, feedback: "fix it" }),
  );
  assert.ok(dec);
  assert.equal(obj(marshalRunDecision({ step: "s1", verb: DecisionAdvance }))["$type"], "run.decision");
  assert.equal(dec!.verb, DecisionRedo);
  assert.equal(dec!.feedback, "fix it");
  assert.equal(parseRunDecision({ $type: "run.review", step: "s1" }), null);
});

test("isDecisionVerb recognises EXACTLY the four v1 verbs and rejects graph reshaping", () => {
  for (const v of [DecisionAdvance, DecisionRedo, DecisionEdit, DecisionStop]) {
    assert.equal(isDecisionVerb(v), true, `${v} should be a valid v1 verb`);
  }
  for (const v of ["branch", "insert", "skip", "", "ADVANCE"]) {
    assert.equal(isDecisionVerb(v), false, `${v} must be rejected (no graph reshaping in v1)`);
  }
  assert.equal(runReviewSubject("01H"), "msg.workflow.run.01H.review");
  assert.equal(runDecisionSubject("01H"), "msg.workflow.run.01H.decision");
});

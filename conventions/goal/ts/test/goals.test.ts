// Unit tests for the TS goals convention's verb logic and read side — the peer of
// the Go suite's goals_test.go. They exercise SetCriterion's get→update→publish,
// its idempotence and absent-criterion no-op, the error steps, and the proof-filter
// / derived rollup, all without a bus (a fake Ops). The byte-for-byte op-transcript
// parity with Go is pinned separately by conformance.test.ts; these guard the
// behaviour the transcript does not (idempotence, errors, the read-model rules).

import { test } from "node:test";
import assert from "node:assert/strict";
import type { JSONValue } from "@sextant/sdk";
import {
  setCriterion,
  SetCriterionError,
  GoalsSubject,
  StatusBlocked,
  StatusInProgress,
  StatusMet,
  StatusNotStarted,
  StatusWaitingOnYou,
  parseGoal,
  criterionMet,
  effectiveStatus,
  provedCriteria,
  rollup,
  type Goal,
  type Ops,
  type Update,
} from "../src/index.js";

// FakeOps is a minimal in-memory Ops: one seeded goal record plus a captured
// publish, enough to exercise SetCriterion's get→update→publish without a bus —
// the peer of Go's fakeOps.
class FakeOps implements Ops {
  record: JSONValue;
  revision: number;
  getErr?: Error;
  updErr?: Error;
  pubErr?: Error;

  updated?: JSONValue;
  updatedRev = 0;
  published?: JSONValue;
  pubSubject = "";
  updateCalls = 0;
  pubCalls = 0;

  constructor(record: JSONValue, revision: number) {
    this.record = record;
    this.revision = revision;
  }

  async getArtifact(_name: string): Promise<{ record: JSONValue; revision: number }> {
    if (this.getErr) throw this.getErr;
    return { record: this.record, revision: this.revision };
  }

  async updateArtifact(_name: string, record: JSONValue, expectedRev: number): Promise<number> {
    this.updateCalls++;
    if (this.updErr) throw this.updErr;
    this.updated = record;
    this.updatedRev = expectedRev;
    return expectedRev + 1;
  }

  async publish(subject: string, record: JSONValue): Promise<void> {
    this.pubCalls++;
    this.pubSubject = subject;
    this.published = record;
    if (this.pubErr) throw this.pubErr;
  }
}

function sampleGoal(): JSONValue {
  return {
    $type: "goal",
    northstar: "Ship the goals convention",
    criteria: [
      { id: "c1", text: "types generated from the lexicon", status: StatusInProgress, owner: "sirius" },
      { id: "c2", text: "both halves consume conv/goals", status: StatusNotStarted },
    ],
  };
}

test("setCriterion writes the rewritten goal and announces the transition", async () => {
  const f = new FakeOps(sampleGoal(), 4);
  const changed = await setCriterion(
    f,
    { goalId: "g1", criterionId: "c1", status: StatusWaitingOnYou, headline: "needs your eyes", ref: "the-pr" },
    "2026-06-19T00:00:00Z",
  );
  assert.equal(changed, true, "changed should be true");
  // The update CAS'd against the get's revision and rewrote c1's status, leaving c2
  // and the north-star untouched.
  assert.equal(f.updatedRev, 4, "update expectedRev should be the get's revision");
  const g = parseGoal(f.updated!);
  assert.ok(g, "the updated record is a goal");
  assert.equal(g!.northstar, "Ship the goals convention", "north-star preserved");
  assert.equal(g!.criteria[0]!.status, StatusWaitingOnYou, "c1 flipped");
  assert.equal(g!.criteria[0]!.text, "types generated from the lexicon", "c1 text preserved");
  assert.equal(g!.criteria[0]!.owner, "sirius", "c1 owner preserved");
  assert.equal(g!.criteria[1]!.status, StatusNotStarted, "c2 untouched");
  // The announcement went out on the goals topic as a goal.update.
  assert.equal(f.pubSubject, GoalsSubject, "publish subject is the goals topic");
  const up = f.published as unknown as Update;
  assert.equal(up.$type, "goal.update");
  assert.equal(up.goal, "g1");
  assert.equal(up.crit, "c1");
  assert.equal(up.status, StatusWaitingOnYou);
  assert.equal(up.ref, "the-pr");
});

test("setCriterion is idempotent: setting the current status is a no-op", async () => {
  const f = new FakeOps(sampleGoal(), 4);
  // c1 is already in-progress; setting it to in-progress writes/announces nothing.
  const changed = await setCriterion(
    f,
    { goalId: "g1", criterionId: "c1", status: StatusInProgress, headline: "x" },
    "",
  );
  assert.equal(changed, false, "a no-op set reports changed=false");
  assert.equal(f.updateCalls, 0, "a no-op set does not update");
  assert.equal(f.pubCalls, 0, "a no-op set does not announce");
});

test("setCriterion on an absent criterion is a no-op", async () => {
  const f = new FakeOps(sampleGoal(), 4);
  const changed = await setCriterion(
    f,
    { goalId: "g1", criterionId: "nope", status: StatusMet, headline: "x" },
    "",
  );
  assert.equal(changed, false, "absent criterion is a no-op");
  assert.equal(f.updateCalls, 0, "absent criterion does not update");
});

test("setCriterion wraps a get failure with step=get and does not write", async () => {
  const f = new FakeOps(sampleGoal(), 4);
  f.getErr = new Error("bus down");
  await assert.rejects(
    () => setCriterion(f, { goalId: "g1", criterionId: "c1", status: StatusMet, headline: "x" }, ""),
    (e: unknown) => e instanceof SetCriterionError && e.step === "get",
  );
  assert.equal(f.updateCalls, 0, "a get failure must not write");
});

test("setCriterion wraps an update failure with step=update (the only retryable step)", async () => {
  const f = new FakeOps(sampleGoal(), 4);
  f.updErr = new Error("revision moved");
  await assert.rejects(
    () => setCriterion(f, { goalId: "g1", criterionId: "c1", status: StatusMet, headline: "x" }, ""),
    (e: unknown) => e instanceof SetCriterionError && e.step === "update",
  );
  assert.equal(f.pubCalls, 0, "a failed update must not announce");
});

test("setCriterion wraps a publish failure with step=publish (the write already landed)", async () => {
  const f = new FakeOps(sampleGoal(), 4);
  f.pubErr = new Error("topic unreachable");
  await assert.rejects(
    () => setCriterion(f, { goalId: "g1", criterionId: "c1", status: StatusMet, headline: "x" }, ""),
    (e: unknown) => e instanceof SetCriterionError && e.step === "publish",
  );
  assert.equal(f.updateCalls, 1, "the write landed before the announce failed");
});

// --- the proof-filter and the derived rollup ---

test("the proof-filter: met needs >=1 proof relation", () => {
  const c = { id: "c1", text: "done", status: StatusMet };
  assert.equal(criterionMet(c, new Set()), false, "met with no proof is not met");
  assert.equal(effectiveStatus(c, new Set()), StatusInProgress, "unproved met reads in-progress");
  const proved = new Set(["c1"]);
  assert.equal(criterionMet(c, proved), true, "met WITH proof is met");
  assert.equal(effectiveStatus(c, proved), StatusMet, "proved met reads met");
});

test("provedCriteria counts only proof relations, not soft related ones", () => {
  const proofArt: JSONValue = { title: "the pr", relates: [{ goal: "g1", crit: "c1", kind: "proof" }] };
  const softArt: JSONValue = { title: "a note", relates: [{ goal: "g1", crit: "c2", kind: "related" }] };
  const proved = provedCriteria("g1", [proofArt, softArt]);
  assert.equal(proved.has("c1"), true, "c1 is proved (a proof artifact relates)");
  assert.equal(proved.has("c2"), false, "c2 is not proved (only a soft relation)");
});

test("rollup derives goal status from the criteria, applying the proof-filter", () => {
  const g: Goal = {
    northstar: "ship it",
    criteria: [
      { id: "c1", text: "", status: StatusMet }, // proved below → met
      { id: "c2", text: "", status: StatusMet }, // NOT proved → in-progress
      { id: "c3", text: "", status: StatusWaitingOnYou }, // waiting
      { id: "c4", text: "", status: StatusBlocked }, // blocked
    ],
  };
  const r = rollup(g, new Set(["c1"]));
  assert.equal(r.total, 4);
  assert.equal(r.met, 1, "only c1 is proved");
  assert.equal(r.waiting, 1);
  assert.equal(r.blocked, true);
  assert.equal(r.defined, true, "has north-star + criteria");
});

test("rollup: undefined without a north-star or without criteria", () => {
  assert.equal(rollup({ northstar: "x", criteria: [] }, new Set()).defined, false, "no criteria");
  assert.equal(
    rollup({ northstar: "", criteria: [{ id: "c1", text: "", status: StatusNotStarted }] }, new Set()).defined,
    false,
    "no north-star",
  );
});

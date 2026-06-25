// Unit tests for the review convention — the behaviour the dashapi review.go +
// review_test.go used to cover, now in TS (ADR-0044): the read-merge-CAS persists
// the verdict preserving other fields, a state-only producer block round-trips
// without phantom verdict attribution, an approve runs the closed loop to flip a
// proof-related goal criterion to met, and the closed loop is best-effort (a
// failure there never fails the verdict). A multi-artifact in-memory Ops stands in
// for the bus.

import { test } from "node:test";
import assert from "node:assert/strict";
import type { JSONValue } from "@sextant/sdk";
import { GoalsSubject, StatusMet } from "@sextant/conv-goals";
import { setReview, mergeReview, type Ops } from "../src/index.js";

const NOW = "2026-06-19T00:00:00Z";

// StoreOps is a multi-artifact in-memory Ops: a name→{record,revision} map plus a
// captured publish log, enough to exercise setReview's CAS AND the closed loop's
// separate goal.<id> write without a bus.
class StoreOps implements Ops {
  arts = new Map<string, { record: JSONValue; revision: number }>();
  published: { subject: string; record: JSONValue }[] = [];
  // forceConflictOnce makes the first updateArtifact for this name throw (a lost
  // CAS), so the retry path is exercised.
  conflictOnce = new Set<string>();
  updateCalls = new Map<string, number>();

  seed(name: string, record: JSONValue, revision: number): void {
    this.arts.set(name, { record, revision });
  }

  async getArtifact(name: string): Promise<{ record: JSONValue; revision: number }> {
    const a = this.arts.get(name);
    if (!a) throw new Error("not found: " + name);
    return { record: a.record, revision: a.revision };
  }

  async updateArtifact(name: string, record: JSONValue, expectedRev: number): Promise<number> {
    this.updateCalls.set(name, (this.updateCalls.get(name) ?? 0) + 1);
    const a = this.arts.get(name);
    if (!a) throw new Error("not found: " + name);
    if (this.conflictOnce.has(name)) {
      this.conflictOnce.delete(name);
      a.revision++; // a concurrent write moved the revision
      throw new Error("revision conflict");
    }
    if (a.revision !== expectedRev) throw new Error("revision conflict");
    a.record = record;
    a.revision++;
    return a.revision;
  }

  async publish(subject: string, record: JSONValue): Promise<void> {
    this.published.push({ subject, record });
  }
}

function obj(v: JSONValue): { [k: string]: JSONValue } {
  return v as { [k: string]: JSONValue };
}

test("setReview persists the verdict, preserving other top-level fields", async () => {
  const ops = new StoreOps();
  ops.seed("brief", { $type: "doc", title: "the brief", body: "x", review: { state: "review" } }, 3);

  const res = await setReview(ops, { name: "brief", state: "approved", by: "01OPERATOR", now: NOW });
  assert.equal(res.review, "approved");
  assert.equal(res.revision, 4);

  const after = obj((await ops.getArtifact("brief")).record);
  assert.equal(after["title"], "the brief"); // preserved
  assert.equal(after["body"], "x"); // preserved
  const rb = obj(after["review"]!);
  assert.equal(rb["state"], "approved");
  assert.equal(rb["by"], "01OPERATOR");
  assert.equal(rb["at"], NOW);
  assert.equal(rb["rev"], 3); // the revision the verdict was made against
});

test("setReview retries once on a CAS conflict", async () => {
  const ops = new StoreOps();
  ops.seed("brief", { $type: "doc", review: { state: "review" } }, 1);
  ops.conflictOnce.add("brief");

  const res = await setReview(ops, { name: "brief", state: "changes", by: "01OP", now: NOW });
  assert.equal(res.review, "changes");
  assert.equal(ops.updateCalls.get("brief"), 2); // first failed, retry succeeded
});

test("a state-only intent (no verdict) round-trips without phantom by/at/rev when re-set to draft", async () => {
  const ops = new StoreOps();
  ops.seed("brief", { $type: "doc", body: "y" }, 1);
  // mergeReview directly with a state-only block (a producer marking needs-review):
  const merged = obj(mergeReview({ $type: "doc", body: "y" }, { state: "review" }));
  const rb = obj(merged["review"]!);
  assert.equal(rb["state"], "review");
  assert.equal(rb["by"], undefined);
  assert.equal(rb["at"], undefined);
  assert.equal(rb["rev"], undefined);
});

test("approve runs the closed loop: a proof-related goal criterion flips to met + announces", async () => {
  const ops = new StoreOps();
  // The approved artifact declares a proof relation backing goal g1 / crit c1.
  ops.seed(
    "the-proof",
    { $type: "doc", title: "PR #42", relates: [{ goal: "g1", crit: "c1", kind: "proof" }], review: { state: "review" } },
    2,
  );
  // The goal it backs (c1 not yet met).
  ops.seed(
    "goal.g1",
    { $type: "goal", northstar: "ship it", criteria: [{ id: "c1", text: "merged", status: "in-progress" }] },
    5,
  );

  const res = await setReview(ops, { name: "the-proof", state: "approved", by: "01OPERATOR", now: NOW });
  assert.equal(res.review, "approved");
  assert.deepEqual(res.advanced, [{ goal: "g1", crit: "c1" }]);

  // The goal criterion moved to met.
  const goal = obj((await ops.getArtifact("goal.g1")).record);
  const crit = obj((goal["criteria"] as JSONValue[])[0]!);
  assert.equal(crit["status"], StatusMet);

  // A goal.update was announced on the goals topic.
  const announce = ops.published.find((p) => p.subject === GoalsSubject);
  assert.ok(announce, "a goal.update was published on msg.topic.goals");
  assert.equal(obj(announce!.record)["goal"], "g1");
  assert.equal(obj(announce!.record)["status"], StatusMet);
});

test("the closed loop is best-effort: a missing goal artifact never fails the approve", async () => {
  const ops = new StoreOps();
  ops.seed(
    "the-proof",
    { $type: "doc", relates: [{ goal: "ghost", crit: "c9", kind: "proof" }], review: { state: "review" } },
    1,
  );
  // goal.ghost is NOT seeded — the closed loop's get will fail, swallowed.
  const res = await setReview(ops, { name: "the-proof", state: "approved", by: "01OP", now: NOW });
  assert.equal(res.review, "approved"); // verdict still succeeded
  assert.deepEqual(res.advanced, []); // nothing advanced
});

test("a non-approve verdict does not run the closed loop", async () => {
  const ops = new StoreOps();
  ops.seed(
    "the-proof",
    { $type: "doc", relates: [{ goal: "g1", crit: "c1", kind: "proof" }], review: { state: "review" } },
    1,
  );
  ops.seed("goal.g1", { $type: "goal", northstar: "x", criteria: [{ id: "c1", text: "t", status: "in-progress" }] }, 1);
  const res = await setReview(ops, { name: "the-proof", state: "changes", by: "01OP", now: NOW });
  assert.deepEqual(res.advanced, []);
  // goal untouched.
  const goal = obj((await ops.getArtifact("goal.g1")).record);
  assert.equal(obj((goal["criteria"] as JSONValue[])[0]!)["status"], "in-progress");
});

// The live co-equality scenario (AC#4 — the real proof, not two suites
// independently green): on ONE real bus, the TS goals convention and the Go goals
// convention write/read the SAME goal artifact, and the record shapes are asserted
// BYTE-IDENTICAL in both directions. This is what "the protocol is language-neutral"
// means operationally: two languages, one lexicon contract, identical wire records.
//
// The proof drives the REAL write path on each side — the TS setCriterion verb
// (get → compare-and-set → publish goal.update) and the Go goals.SetCriterion via a
// helper binary (test/gohelper) — never a hand-rolled artifact write. "Byte-
// identical" is measured with the canonical-JSON rule (FORMAT.md): the SDK's
// `canonical` on the TS side and protocol/conformance.Canonicalize on the Go side,
// the same rule, so the comparison is a genuine cross-language byte claim.
//
// Gated on the Go toolchain being present (skip-with-reason when `go` is not on
// PATH) so the unit/conformance tests still run everywhere.

import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { connect, canonical, type Client, type JSONValue } from "@sextant/sdk";
import { setCriterion, artifactName, StatusMet } from "../src/index.js";
import { startBus, goAvailable, type Bus } from "./harness.js";

const skip = !goAvailable();
const skipReason = "the `go` toolchain is not on PATH (the live co-equality scenario needs the real Go bus + convention)";

// fixedNow is the timestamp both sides stamp, so a Go-written and a TS-written goal
// record carry the same `updated` value and canonicalize identically. The Go helper
// uses the same constant.
const fixedNow = "2026-06-19T00:00:00Z";

// fixedGoal is the shared starting record both languages seed — the exact TS peer
// of the Go helper's fixedGoal(). The criterion the scenario flips (c1) starts
// not-started; the sibling (c2) is in-progress. c1 carries an owner; c2 does not
// (Go's omitempty drops it, so the TS record must omit it too for byte-parity).
function fixedGoal(): JSONValue {
  return {
    northstar: "Prove the goals convention is co-equal across languages",
    stream: "m6",
    criteria: [
      { id: "c1", text: "TS and Go write the same goal record", status: "not-started", owner: "sirius" },
      { id: "c2", text: "byte-identical canonical bytes", status: "in-progress" },
    ],
    updated: fixedNow,
  };
}

// expectedAfterFlip is the goal record after c1 is flipped to met — what BOTH the
// TS verb and the Go verb must produce from the shared starting record. It is the
// independent oracle: each direction is checked against it AND against the other
// language's actual output.
function expectedAfterFlip(): JSONValue {
  const g = fixedGoal() as { criteria: Array<{ id: string; status: string }> };
  g.criteria[0]!.status = StatusMet;
  return g as unknown as JSONValue;
}

let bus: Bus;
let tsCreds: string;
let goCreds: string;

before(() => {
  if (skip) return;
  bus = startBus();
  tsCreds = bus.mint("ts-goals", "agent").credsPath;
  goCreds = bus.mint("go-goals", "agent").credsPath;
});

after(() => {
  if (skip || !bus) return;
  bus.stop();
});

test("co-equality A: a goal the TS convention writes is read byte-identical by the Go convention", { skip: skip && skipReason }, async () => {
  const c: Client = await connect({ credsPath: tsCreds, url: bus.url });
  try {
    const goalId = "ts-written";
    const name = artifactName(goalId);

    // TS writes: create the shared goal, then run the TS setCriterion verb (the real
    // write path: get → CAS → publish goal.update) to flip c1 to met.
    await c.createArtifact(name, fixedGoal());
    const changed = await setCriterion(
      c,
      { goalId, criterionId: "c1", status: StatusMet, headline: "TS flips c1", by: "ts-goals" },
      fixedNow,
    );
    assert.equal(changed, true, "the TS verb flipped the criterion");

    // The independent oracle: the TS-written record canonicalizes to the expected
    // post-flip shape.
    const wantCanon = canonical(expectedAfterFlip());

    // TS reads its own write back and canonicalizes.
    const tsRead = await c.getArtifact(name);
    const tsCanon = canonical(tsRead.record);
    assert.equal(tsCanon, wantCanon, "the TS read-back matches the expected post-flip record");

    // Go reads the SAME artifact through the Go goals convention helper and prints
    // its canonical record (protocol/conformance.Canonicalize — the same rule).
    const go = bus.runGo(["read", name], goCreds);
    assert.equal(go.code, 0, `go read failed: ${go.stderr}`);
    const goCanon = go.stdout.trim();

    // The keystone: the record the Go convention reads is byte-identical to what the
    // TS convention wrote. Co-equality, direction A.
    assert.equal(goCanon, tsCanon, "Go reads byte-identical bytes to what TS wrote");
    assert.equal(goCanon, wantCanon, "and both match the language-neutral oracle");
    // Surface the actual bytes in the test log (the PR evidence).
    console.error(`[co-equality A] TS wrote == Go read (byte-identical):\n  ${goCanon}`);
  } finally {
    await c.close();
  }
});

test("co-equality B: a goal the Go convention writes is read byte-identical by the TS convention", { skip: skip && skipReason }, async () => {
  const c: Client = await connect({ credsPath: tsCreds, url: bus.url });
  try {
    const goalId = "go-written";
    const name = artifactName(goalId);

    // Go writes: seed the shared goal, then run goals.SetCriterion to flip c1 to met
    // (the Go convention's real write path). The helper prints the resulting
    // canonical goal record.
    const seedR = bus.runGo(["seed", goalId], goCreds);
    assert.equal(seedR.code, 0, `go seed failed: ${seedR.stderr}`);
    const setR = bus.runGo(["set", goalId, "c1", StatusMet, "Go flips c1"], goCreds);
    assert.equal(setR.code, 0, `go set failed: ${setR.stderr}`);
    const goCanon = setR.stdout.trim();

    // The independent oracle.
    const wantCanon = canonical(expectedAfterFlip());

    // TS reads the SAME artifact and canonicalizes with the SDK's `canonical`.
    const tsRead = await c.getArtifact(name);
    const tsCanon = canonical(tsRead.record);

    // The keystone: the record the TS convention reads is byte-identical to what the
    // Go convention wrote. Co-equality, direction B.
    assert.equal(tsCanon, goCanon, "TS reads byte-identical bytes to what Go wrote");
    assert.equal(tsCanon, wantCanon, "and both match the language-neutral oracle");
    console.error(`[co-equality B] Go wrote == TS read (byte-identical):\n  ${tsCanon}`);
  } finally {
    await c.close();
  }
});

test("co-equality round-trip: TS and Go agree on the goal artifact across a write from each side", { skip: skip && skipReason }, async () => {
  // A combined assertion the reviewer can read at a glance: both directions land on
  // the SAME canonical record. If A and B each equal the oracle, they equal each
  // other — but assert it directly so a regression in either path is loud.
  const c: Client = await connect({ credsPath: tsCreds, url: bus.url });
  try {
    const oracle = canonical(expectedAfterFlip());

    // TS-written (reuse direction A's artifact, freshly written here to be self-
    // contained) and Go-written records both read back equal to the oracle.
    const tsName = artifactName("rt-ts");
    await c.createArtifact(tsName, fixedGoal());
    await setCriterion(c, { goalId: "rt-ts", criterionId: "c1", status: StatusMet, headline: "x" }, fixedNow);
    const tsBytes = canonical((await c.getArtifact(tsName)).record);

    const goName = "rt-go";
    assert.equal(bus.runGo(["seed", goName], goCreds).code, 0);
    const goSet = bus.runGo(["set", goName, "c1", StatusMet, "x"], goCreds);
    assert.equal(goSet.code, 0, `go set failed: ${goSet.stderr}`);
    const goBytes = goSet.stdout.trim();

    assert.equal(tsBytes, oracle, "TS write == oracle");
    assert.equal(goBytes, oracle, "Go write == oracle");
    assert.equal(tsBytes, goBytes, "TS write == Go write (byte-identical record shapes)");
  } finally {
    await c.close();
  }
});

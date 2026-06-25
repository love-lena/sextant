// The TS goals conformance replay: it runs the REAL TS goal verbs against the TS
// Recorder and asserts the captured operations equal the language-neutral vectors
// under protocol/conformance/vectors/goals — the SAME JSON files the Go suite
// replays (FORMAT.md, ADR-0041). Passing the IDENTICAL setCriterion.json vector
// the Go suite passes is the op-transcript co-equality proof (AC#2).
//
// The comparison reproduces FORMAT.md exactly: order-sensitive ops; per-op op /
// subject / name / expectedRev equality; each payload compared as canonical JSON
// via the SDK's `canonical` (the single rule both languages must agree on).

import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { canonical, type JSONValue } from "@sextant/sdk";
import {
  setCriterion,
  artifactName,
  StatusInProgress,
  StatusNotStarted,
  type SetCriterionInput,
} from "../src/index.js";
import { Recorder, type Op } from "./recorder.js";
import { goalVectorsDir } from "./repoRoot.js";

// fixedNow is the timestamp the recorded setCriterion verb stamps, so the captured
// goal.update is byte-stable and matches the vector's `updated` field. The peer of
// Go's fixedNow in conformance_test.go. The live SetCriterion takes the real time;
// only the recorded/replayed verb pins it.
const fixedNow = "2026-06-19T00:00:00Z";

// OpTranscriptVector is the on-disk vector shape (protocol/conformance/vector.go's
// OpTranscriptVector / FORMAT.md). input is opaque to the format; the verb decodes
// it.
interface OpTranscriptVector {
  epoch: number;
  convention: string;
  verb: string;
  description?: string;
  input: JSONValue;
  operations: Op[];
}

function loadGoalVectors(): Array<{ path: string; vector: OpTranscriptVector }> {
  const dir = goalVectorsDir();
  const files = readdirSync(dir).filter((f) => f.endsWith(".json"));
  if (files.length === 0) {
    throw new Error(`no goals vectors found under ${dir}`);
  }
  return files
    .sort()
    .map((f) => {
      const path = join(dir, f);
      return { path, vector: JSON.parse(readFileSync(path, "utf8")) as OpTranscriptVector };
    });
}

// runVerb dispatches (convention, verb) to the registered TS verb, run against the
// recorder. Only setCriterion exists today; an unknown verb fails loudly (a vector
// no verb answers is a gap, not a pass — mirroring the Go runner).
async function runVerb(rec: Recorder, v: OpTranscriptVector): Promise<void> {
  if (v.convention !== "goals") {
    throw new Error(`vector names convention ${JSON.stringify(v.convention)}, not goals`);
  }
  switch (v.verb) {
    case "setCriterion": {
      const input = v.input as unknown as SetCriterionInput;
      await setCriterion(rec, input, fixedNow);
      return;
    }
    default:
      throw new Error(`vector names verb ${JSON.stringify(v.verb)}, which is not registered`);
  }
}

// seedGoal seeds the recorder with the prior goal artifact a setCriterion vector
// reads before it writes (a read-then-write verb). The seed mirrors the bus state
// it would find live and does NOT appear in the transcript. It is the exact peer of
// the Go suite's seedGoal: a goal with the target criterion (starting not-started,
// owner sirius) plus a sibling (in-progress), at revision 4 — so the transcript
// shows the rewrite preserves siblings and the north-star and CAS's against rev 4.
function seedGoal(rec: Recorder, v: OpTranscriptVector): void {
  if (v.verb !== "setCriterion") return;
  const input = v.input as unknown as SetCriterionInput;
  const goal: JSONValue = {
    northstar: "Ship the goals convention",
    criteria: [
      { id: input.criterionId, text: "the criterion under test", status: StatusNotStarted, owner: "sirius" },
      { id: "other", text: "a sibling criterion", status: StatusInProgress },
    ],
  };
  rec.seedArtifact(artifactName(input.goalId), goal, 4);
}

// canonicalPayload canonicalizes an op's payload under the FORMAT.md rule. An
// absent payload canonicalizes to "null" (so two ops that both omit a payload
// compare equal) — mirroring Go's Canonicalize(nil) == "null".
function canonicalPayload(p: JSONValue | undefined): string {
  return canonical(p === undefined ? null : p);
}

// assertOpsEqual compares the recorded transcript to the vector's, order-sensitive,
// each payload under the canonical-JSON rule — the TS peer of the Go runner's
// assertOpsEqual.
function assertOpsEqual(path: string, want: Op[], got: Op[]): void {
  assert.equal(
    got.length,
    want.length,
    `${path}: operation count\n want: ${JSON.stringify(want.map((o) => o.op))}\n got:  ${JSON.stringify(got.map((o) => o.op))}`,
  );
  for (let i = 0; i < want.length; i++) {
    const w = want[i]!;
    const g = got[i]!;
    assert.equal(g.op, w.op, `${path}: op[${i}].op`);
    assert.equal(g.subject ?? "", w.subject ?? "", `${path}: op[${i}] (${w.op}).subject`);
    assert.equal(g.name ?? "", w.name ?? "", `${path}: op[${i}] (${w.op}).name`);
    assert.equal(
      g.expectedRev ?? null,
      w.expectedRev ?? null,
      `${path}: op[${i}] (${w.op}).expectedRev`,
    );
    const cw = canonicalPayload(w.payload);
    const cg = canonicalPayload(g.payload);
    assert.equal(cg, cw, `${path}: op[${i}] (${w.op}).payload mismatch\n want: ${cw}\n got:  ${cg}`);
  }
}

const vectors = loadGoalVectors();

test("the goals conformance vectors are discovered", () => {
  assert.ok(vectors.length >= 1, "expected at least one goals vector");
  // The setCriterion.json vector the Go suite passes must be present — the
  // identical file is the co-equality contract.
  assert.ok(
    vectors.some((v) => v.path.endsWith("setCriterion.json")),
    "expected the setCriterion.json vector the Go suite passes",
  );
});

for (const { path, vector } of vectors) {
  const rel = path.split("/").slice(-2).join("/");
  test(`goals vector ${rel} replays to the identical operations`, async () => {
    assert.equal(vector.epoch, 1, "the shipped goals vector is pinned to epoch 1");
    const rec = new Recorder();
    seedGoal(rec, vector);
    await runVerb(rec, vector);
    assertOpsEqual(path, vector.operations, rec.operations());
  });
}

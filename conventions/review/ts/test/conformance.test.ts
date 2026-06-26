// The TS review conformance replay: it runs the REAL TS review verb against the TS
// Recorder and asserts the captured operations equal the language-neutral vectors
// under protocol/conformance/vectors/review — the SAME JSON files the Go suite
// replays (FORMAT.md, ADR-0041). Passing the IDENTICAL setReview.json +
// setReviewApprove.json vectors the Go suite passes is the op-transcript
// co-equality proof (TASK-239 AC#2/AC#9).
//
// The comparison reproduces FORMAT.md exactly: order-sensitive ops; per-op op /
// subject / name / expectedRev equality; each payload compared as canonical JSON
// via the SDK's `canonical`.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { canonical, type JSONValue } from "@sextant/sdk";
import { setReview, type SetReviewInput } from "../src/index.js";
import { Recorder, type Op } from "./recorder.js";
import { reviewVectorsDir } from "./repoRoot.js";

// OpTranscriptVector is the on-disk vector shape (protocol/conformance/vector.go's
// OpTranscriptVector / FORMAT.md). input is opaque to the format; the verb decodes it.
interface OpTranscriptVector {
  epoch: number;
  convention: string;
  verb: string;
  description?: string;
  input: JSONValue;
  operations: Op[];
}

function loadReviewVectors(): Array<{ path: string; vector: OpTranscriptVector }> {
  const dir = reviewVectorsDir();
  const files = readdirSync(dir).filter((f) => f.endsWith(".json"));
  if (files.length === 0) {
    throw new Error(`no review vectors found under ${dir}`);
  }
  return files
    .sort()
    .map((f) => {
      const path = join(dir, f);
      return { path, vector: JSON.parse(readFileSync(path, "utf8")) as OpTranscriptVector };
    });
}

// runVerb dispatches (convention, verb) to the registered TS verb, run against the
// recorder. Only setReview exists; an unknown verb fails loudly (a vector no verb
// answers is a gap, not a pass — mirroring the Go runner).
async function runVerb(rec: Recorder, v: OpTranscriptVector): Promise<void> {
  if (v.convention !== "review") {
    throw new Error(`vector names convention ${JSON.stringify(v.convention)}, not review`);
  }
  switch (v.verb) {
    case "setReview": {
      const input = v.input as unknown as SetReviewInput;
      await setReview(rec, input);
      return;
    }
    default:
      throw new Error(`vector names verb ${JSON.stringify(v.verb)}, which is not registered`);
  }
}

// seedReview seeds the recorder with the prior bus state a setReview vector reads
// before it writes — the exact peer of the Go suite's seedReview. For an approve it
// also seeds the goal the artifact's proof relation backs, so the closed-loop
// transcript is reproduced. The seed does NOT appear in the transcript.
function seedReview(rec: Recorder, v: OpTranscriptVector): void {
  if (v.verb !== "setReview") return;
  const input = v.input as unknown as SetReviewInput;
  if (input.state === "approved") {
    rec.seedArtifact(
      input.name,
      { $type: "doc", title: "PR #42", relates: [{ goal: "g1", crit: "c1", kind: "proof" }], review: { state: "review" } },
      2,
    );
    rec.seedArtifact(
      "goal.g1",
      { $type: "goal", northstar: "ship it", criteria: [{ id: "c1", text: "merged", status: "in-progress" }] },
      5,
    );
    return;
  }
  rec.seedArtifact(input.name, { $type: "doc", title: "the brief", body: "x", review: { state: "review" } }, 3);
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
    assert.equal(g.expectedRev ?? null, w.expectedRev ?? null, `${path}: op[${i}] (${w.op}).expectedRev`);
    const cw = canonicalPayload(w.payload);
    const cg = canonicalPayload(g.payload);
    assert.equal(cg, cw, `${path}: op[${i}] (${w.op}).payload mismatch\n want: ${cw}\n got:  ${cg}`);
  }
}

const vectors = loadReviewVectors();

test("the review conformance vectors are discovered", () => {
  assert.ok(vectors.length >= 1, "expected at least one review vector");
  assert.ok(
    vectors.some((v) => v.path.endsWith("setReviewApprove.json")),
    "expected the setReviewApprove.json vector the Go suite passes",
  );
});

for (const { path, vector } of vectors) {
  const rel = path.split("/").slice(-2).join("/");
  test(`review vector ${rel} replays to the identical operations`, async () => {
    assert.equal(vector.epoch, 1, "the shipped review vector is pinned to epoch 1");
    const rec = new Recorder();
    seedReview(rec, vector);
    await runVerb(rec, vector);
    assertOpsEqual(path, vector.operations, rec.operations());
  });
}

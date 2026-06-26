// The TS workflow conformance replay: it runs the REAL TS requestWorkflowStart verb
// against the TS Recorder and asserts the captured operation equals the
// language-neutral vector under protocol/conformance/vectors/workflow — the SAME
// JSON the Go suite replays (FORMAT.md, ADR-0041). Passing the IDENTICAL
// requestWorkflowStart.json the Go suite passes is the op-transcript co-equality
// proof (TASK-239 AC#7/AC#9).

import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { canonical, type JSONValue } from "@sextant/sdk";
import { requestWorkflowStart, type WorkflowStartRequest } from "../src/index.js";
import { Recorder, type Op } from "./recorder.js";
import { workflowVectorsDir } from "./repoRoot.js";

interface OpTranscriptVector {
  epoch: number;
  convention: string;
  verb: string;
  description?: string;
  input: JSONValue;
  operations: Op[];
}

function loadWorkflowVectors(): Array<{ path: string; vector: OpTranscriptVector }> {
  const dir = workflowVectorsDir();
  const files = readdirSync(dir).filter((f) => f.endsWith(".json"));
  if (files.length === 0) {
    throw new Error(`no workflow vectors found under ${dir}`);
  }
  return files
    .sort()
    .map((f) => {
      const path = join(dir, f);
      return { path, vector: JSON.parse(readFileSync(path, "utf8")) as OpTranscriptVector };
    });
}

async function runVerb(rec: Recorder, v: OpTranscriptVector): Promise<void> {
  if (v.convention !== "workflow") {
    throw new Error(`vector names convention ${JSON.stringify(v.convention)}, not workflow`);
  }
  switch (v.verb) {
    case "requestWorkflowStart": {
      const input = v.input as unknown as WorkflowStartRequest;
      await requestWorkflowStart(rec, input);
      return;
    }
    default:
      throw new Error(`vector names verb ${JSON.stringify(v.verb)}, which is not registered`);
  }
}

function canonicalPayload(p: JSONValue | undefined): string {
  return canonical(p === undefined ? null : p);
}

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

const vectors = loadWorkflowVectors();

test("the workflow conformance vectors are discovered", () => {
  assert.ok(vectors.length >= 1, "expected at least one workflow vector");
  assert.ok(
    vectors.some((v) => v.path.endsWith("requestWorkflowStart.json")),
    "expected the requestWorkflowStart.json vector the Go suite passes",
  );
});

for (const { path, vector } of vectors) {
  const rel = path.split("/").slice(-2).join("/");
  test(`workflow vector ${rel} replays to the identical operations`, async () => {
    assert.equal(vector.epoch, 1, "the shipped workflow vector is pinned to epoch 1");
    const rec = new Recorder();
    await runVerb(rec, vector);
    assertOpsEqual(path, vector.operations, rec.operations());
  });
}

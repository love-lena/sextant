// The TS workflow run/v1 conformance replay (TASK-247): it runs the REAL TS run/v1
// publish verbs against the TS Recorder and asserts the captured operation equals the
// language-neutral vector under protocol/conformance/vectors/workflow — the SAME JSON
// the Go suite replays (FORMAT.md, ADR-0041). Passing the IDENTICAL vectors the Go
// suite passes is the op-transcript co-equality proof for the run/v1 contract, which
// the legacy requestWorkflowStart vector used to give the retired path (TASK-234).
//
// run.event and run.control ride a run-scoped subject, so their vectors carry a runId
// alongside the record fields; the dispatch splits the id (the verb argument) from the
// record fields exactly as the Go suite does.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { canonical, type JSONValue } from "@sextant/sdk";
import {
  requestRunStart,
  emitRunEvent,
  requestRunControl,
  type RunStartRequest,
  type RunEvent,
  type RunControl,
} from "../src/index.js";
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

function obj(v: JSONValue): { [k: string]: JSONValue } {
  return v as { [k: string]: JSONValue };
}

async function runVerb(rec: Recorder, v: OpTranscriptVector): Promise<void> {
  if (v.convention !== "workflow") {
    throw new Error(`vector names convention ${JSON.stringify(v.convention)}, not workflow`);
  }
  const input = obj(v.input);
  switch (v.verb) {
    case "requestRunStart": {
      await requestRunStart(rec, v.input as unknown as RunStartRequest);
      return;
    }
    case "emitRunEvent": {
      await emitRunEvent(rec, input["runId"] as string, input["event"] as unknown as RunEvent);
      return;
    }
    case "requestRunControl": {
      await requestRunControl(rec, input["runId"] as string, input["control"] as unknown as RunControl);
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

test("the workflow run/v1 conformance vectors are discovered", () => {
  assert.ok(vectors.length >= 1, "expected at least one workflow vector");
  for (const verb of ["requestRunStart", "emitRunEvent", "requestRunControl"]) {
    assert.ok(
      vectors.some((v) => v.path.endsWith(`${verb}.json`)),
      `expected the ${verb}.json vector the Go suite passes`,
    );
  }
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

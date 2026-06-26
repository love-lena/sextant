// The TS spawn conformance replay: it runs the REAL TS requestSpawn verb against
// the TS Recorder and asserts the captured operation equals the language-neutral
// vector under protocol/conformance/vectors/spawn — the SAME JSON the Go suite
// replays (FORMAT.md, ADR-0041). Passing the IDENTICAL requestSpawn.json the Go
// suite passes is the op-transcript co-equality proof (TASK-239 AC#8/AC#9).

import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { canonical, type JSONValue } from "@sextant/sdk";
import { requestSpawn, type SpawnRequest } from "../src/index.js";
import { Recorder, type Op } from "./recorder.js";
import { spawnVectorsDir } from "./repoRoot.js";

interface OpTranscriptVector {
  epoch: number;
  convention: string;
  verb: string;
  description?: string;
  input: JSONValue;
  operations: Op[];
}

function loadSpawnVectors(): Array<{ path: string; vector: OpTranscriptVector }> {
  const dir = spawnVectorsDir();
  const files = readdirSync(dir).filter((f) => f.endsWith(".json"));
  if (files.length === 0) {
    throw new Error(`no spawn vectors found under ${dir}`);
  }
  return files
    .sort()
    .map((f) => {
      const path = join(dir, f);
      return { path, vector: JSON.parse(readFileSync(path, "utf8")) as OpTranscriptVector };
    });
}

async function runVerb(rec: Recorder, v: OpTranscriptVector): Promise<void> {
  if (v.convention !== "spawn") {
    throw new Error(`vector names convention ${JSON.stringify(v.convention)}, not spawn`);
  }
  switch (v.verb) {
    case "requestSpawn": {
      const input = v.input as unknown as SpawnRequest;
      await requestSpawn(rec, input);
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

const vectors = loadSpawnVectors();

test("the spawn conformance vectors are discovered", () => {
  assert.ok(vectors.length >= 1, "expected at least one spawn vector");
  assert.ok(
    vectors.some((v) => v.path.endsWith("requestSpawn.json")),
    "expected the requestSpawn.json vector the Go suite passes",
  );
});

for (const { path, vector } of vectors) {
  const rel = path.split("/").slice(-2).join("/");
  test(`spawn vector ${rel} replays to the identical operations`, async () => {
    assert.equal(vector.epoch, 1, "the shipped spawn vector is pinned to epoch 1");
    const rec = new Recorder();
    await runVerb(rec, vector);
    assertOpsEqual(path, vector.operations, rec.operations());
  });
}

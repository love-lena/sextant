// Unit tests for the spawn convention: the wire-record builder omits empty lineage
// (byte-parity with Go), requestSpawn emits exactly one publish on the right
// subject, and parseSpawnAck accepts an ack while rejecting other $types.

import { test } from "node:test";
import assert from "node:assert/strict";
import type { JSONValue } from "@sextant/sdk";
import { spawnRequestRecord, requestSpawn, parseSpawnAck, RequestSubject, type Ops } from "../src/index.js";

function obj(v: JSONValue): { [k: string]: JSONValue } {
  return v as { [k: string]: JSONValue };
}

test("spawnRequestRecord stamps $type and omits empty lineage fields", () => {
  const rec = obj(spawnRequestRecord({ prompt: "x", nickname: "alpha" }));
  assert.equal(rec["$type"], "spawn.request");
  assert.equal(rec["prompt"], "x");
  assert.equal(rec["nickname"], "alpha");
  assert.equal("job" in rec, false, "empty job omitted");
  assert.equal("parent" in rec, false, "empty parent omitted");
});

test("spawnRequestRecord carries set lineage fields", () => {
  const rec = obj(spawnRequestRecord({ prompt: "x", job: "job-7", parent: "01PARENT" }));
  assert.equal(rec["job"], "job-7");
  assert.equal(rec["parent"], "01PARENT");
});

test("requestSpawn emits exactly one publish on RequestSubject", async () => {
  const calls: { subject: string; record: JSONValue }[] = [];
  const ops: Ops = {
    async publish(subject, record) {
      calls.push({ subject, record });
    },
  };
  await requestSpawn(ops, { prompt: "hello", nickname: "alpha" });
  assert.equal(calls.length, 1, "exactly one message.publish");
  assert.equal(calls[0]!.subject, RequestSubject);
  assert.equal(obj(calls[0]!.record)["prompt"], "hello");
});

test("parseSpawnAck accepts an ack and rejects other records", () => {
  const ack = parseSpawnAck({ $type: "spawn.ack", id: "01CHILD", requestId: "01REQ", status: "ok" });
  assert.ok(ack, "ack parsed");
  assert.equal(ack!.id, "01CHILD");
  assert.equal(ack!.requestId, "01REQ");

  assert.equal(parseSpawnAck({ $type: "spawn.request", prompt: "x" }), null, "rejects a spawn.request");
  assert.equal(parseSpawnAck({ $type: "chat.message", text: "hi" }), null, "rejects another $type");
  assert.equal(parseSpawnAck(null), null, "rejects null");
  assert.equal(parseSpawnAck([1, 2]), null, "rejects an array");
});

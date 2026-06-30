// The spawn convention in TypeScript (ADR-0041, TASK-239): a co-equal peer of
// conventions/spawn/go. A spawn.request is an ordinary message record — opaque to
// the bus, carried on a normal msg.* subject — asking a dispatcher to spawn a new
// client. It carries DATA only, never a command to run: the dispatcher launches its
// OWN trusted harness, so a request can never inject code onto the dispatcher's
// host; the only thing it influences is the prompt and the lineage labels.
//
// As an engine-as-a-library (ADR-0011), the verb here translates a domain action
// (request a spawn) into the same primitive operation a bare client could issue —
// one message.publish — reaching the bus only through the Ops seam. The verb LOGIC
// is hand-written (concept, not codegen); the emitted record matches the Go
// convention byte-for-byte, which the shared conformance vector pins (the
// co-equality proof). The dash uses spawnRequestRecord to build the record it posts
// over its own transport, so the wire shape has one source.

import type { JSONValue } from "@sextant/sdk";

// RequestSubject is the well-known subject a dispatcher watches for spawn.request
// records — the default the dispatcher, violet, and the dash all use.
export const RequestSubject = "msg.topic.spawn";

// Ops is the primitive bus surface the spawn verb is written against: a single
// publish (a spawn.request is one fire-and-forget message). Declared minimally and
// where it is consumed, so the SDK Client, a fake, and the dash's publish shim each
// satisfy it. The peer of Go's spawn.Ops.
export interface Ops {
  publish(subject: string, record: JSONValue): Promise<void>;
}

// SpawnRequest is the domain input — the spawn.request record minus its $type
// discriminant (the builder stamps that). The field names mirror Go's SpawnRequest
// exactly. prompt is required; nickname/job/parent are optional lineage labels.
// model is the optional per-step model declaration (TASK-245): when set, the
// dispatcher runs the worker on this model (sets SX_AGENT_MODEL).
// workdir is the worker's scoped working directory (TASK-256): when set, the
// dispatcher exports it as SEXTANT_PI_WORKDIR so the worker runs inside it (a
// run's per-run git worktree). The peer of Go's SpawnRequest.Workdir.
export interface SpawnRequest {
  prompt: string;
  nickname?: string;
  job?: string;
  parent?: string;
  model?: string;
  workdir?: string;
}

// SpawnAck is the dispatcher's acknowledgement of one spawn.request, carrying the
// new client's bus-minted id and the lineage. The peer of Go's SpawnAck.
export interface SpawnAck {
  $type: "spawn.ack";
  id?: string;
  nickname?: string;
  requestId: string;
  job?: string;
  parent?: string;
  status: string;
  error?: string;
}

export const StatusOK = "ok";
export const StatusError = "error";

// spawnRequestRecord builds the spawn.request wire record, stamping $type and
// emitting only the lineage fields that are set — byte-identical to Go's
// SpawnRequestRecord (whose struct omitempty tags drop empty nickname/job/parent).
// parent is never injected: a dispatcher trusts the bus-stamped frame author as the
// true parent, so a caller passing parent is honoured, but the builder adds none.
export function spawnRequestRecord(req: SpawnRequest): JSONValue {
  const rec: { [k: string]: JSONValue } = { $type: "spawn.request", prompt: req.prompt };
  if (req.nickname) rec["nickname"] = req.nickname;
  if (req.job) rec["job"] = req.job;
  if (req.parent) rec["parent"] = req.parent;
  if (req.model) rec["model"] = req.model;
  if (req.workdir) rec["workdir"] = req.workdir;
  return rec;
}

// requestSpawn publishes a spawn.request on subject (default RequestSubject) — the
// single bus operation a requester issues. The op-transcript conformance vector
// pins it to exactly one message.publish; the Go peer emits the identical record.
export async function requestSpawn(ops: Ops, req: SpawnRequest, subject: string = RequestSubject): Promise<void> {
  await ops.publish(subject, spawnRequestRecord(req));
}

// parseSpawnAck decodes a record as a spawn.ack, returning null for any other $type
// (e.g. the requester's own spawn.request echoed back) — the peer of Go's
// ParseSpawnAck. The dash uses it to correlate the dispatcher's reply.
export function parseSpawnAck(record: JSONValue): SpawnAck | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  const r = record as { [k: string]: JSONValue };
  if (r["$type"] !== "spawn.ack") return null;
  return record as unknown as SpawnAck;
}

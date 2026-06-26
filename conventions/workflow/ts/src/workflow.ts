// The workflow-start verb in TypeScript (ADR-0011, TASK-239): a co-equal peer of
// conventions/workflow/go's RequestWorkflowStart. A requester (the browser dash)
// asks the coordinator to start a run by publishing a workflow.start; the
// coordinator owns the CAS-checkpointed state envelope and walks the steps.
//
// As an engine-as-a-library (ADR-0011), the verb translates a domain action (start
// a workflow) into the same primitive operation a bare client could issue — one
// message.publish — reaching the bus only through the Ops seam. The dash uses
// workflowStartRecord to build the record it posts over its own transport, so the
// wire shape has one source; the shared conformance vector pins it byte-identical to
// the Go peer.

import type { JSONValue } from "@sextant/sdk";
import { TypeWorkflowStart, TypeWorkflowStartAck } from "./records.js";

// StartSubject is the well-known subject the workflow coordinator watches for
// workflow.start requests and publishes workflow.start.ack to (the peer of Go's
// StartSubject).
export const StartSubject = "msg.topic.workflow.start";

// Ops is the primitive bus surface the workflow-start verb is written against: a
// single publish. Declared minimally and where it is consumed, so the SDK Client, a
// fake, and the dash's publish shim each satisfy it. The peer of Go's workflow.Ops.
export interface Ops {
  publish(subject: string, record: JSONValue): Promise<void>;
}

// WorkflowStartRequest is the domain input — the workflow.start record minus its
// $type discriminant (the builder stamps that). The field names mirror Go's
// WorkflowStartRequest exactly. prompt is required; nonce is the dash's opaque
// correlation handle (echoed verbatim in the ack); nickname/target/by are labels.
export interface WorkflowStartRequest {
  prompt: string;
  nonce?: string;
  nickname?: string;
  target?: string;
  by?: string;
}

// WorkflowStartAck is the coordinator's reply on StartSubject for every handled
// request (success or failure) — fail-loud. nonce echoes the request's nonce so the
// dash can correlate. The peer of Go's WorkflowStartAck.
export interface WorkflowStartAck {
  $type: "workflow.start.ack";
  nonce?: string;
  requestId: string;
  workflowId?: string;
  status: string;
  error?: string;
}

// workflowStartRecord builds the workflow.start wire record, stamping $type and
// emitting only the fields that are set — byte-identical to Go's WorkflowStartRecord
// (whose struct omitempty tags drop empty nonce/nickname/target/by).
export function workflowStartRecord(req: WorkflowStartRequest): JSONValue {
  const rec: { [k: string]: JSONValue } = { $type: TypeWorkflowStart, prompt: req.prompt };
  if (req.nonce) rec["nonce"] = req.nonce;
  if (req.nickname) rec["nickname"] = req.nickname;
  if (req.target) rec["target"] = req.target;
  if (req.by) rec["by"] = req.by;
  return rec;
}

// requestWorkflowStart publishes a workflow.start on subject (default StartSubject)
// — the single bus operation a requester issues. The op-transcript conformance
// vector pins it to exactly one message.publish; the Go peer emits the identical
// record.
export async function requestWorkflowStart(
  ops: Ops,
  req: WorkflowStartRequest,
  subject: string = StartSubject,
): Promise<void> {
  await ops.publish(subject, workflowStartRecord(req));
}

// parseWorkflowStartAck decodes a record as a workflow.start.ack, returning null for
// any other $type — the dash uses it to correlate the coordinator's reply by nonce.
export function parseWorkflowStartAck(record: JSONValue): WorkflowStartAck | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  const r = record as { [k: string]: JSONValue };
  if (r["$type"] !== TypeWorkflowStartAck) return null;
  return record as unknown as WorkflowStartAck;
}

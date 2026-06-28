// The run contract in TypeScript (ADR-0048) — the co-equal peer of
// conventions/workflow/go/run.go. A RUN is one live instance of work (a ULID with
// steps); a TEMPLATE is the reusable spec. Both are conventions over the two
// primitives: the run envelope is a CAS-checkpointed Artifact (single-writer = the
// coordinator), control/events ride msg.* subjects. The browser dash parses the run
// envelope to render progress and emits run.start / run.control over its own client.
//
// Additive alongside records.ts's older sextant.workflow/v1 contract: it lands the
// ADR-0048 shapes ahead of the coordinator retarget that consumes them.

import type { JSONValue } from "@sextant/sdk";
import { StepDone } from "./records.js";

export const KindRun = "sextant.workflow.run/v1";
export const KindTemplate = "sextant.workflow.template/v1";
export const TypeRunEvent = "run.event";
export const TypeRunControl = "run.control";
export const TypeRunStart = "run.start";
export const TypeRunStartAck = "run.start.ack";

// Run statuses (the dash's RUN_STATUS set; no "failed" — a failed step → blocked).
export const RunRunning = "running";
export const RunWaiting = "waiting";
export const RunBlocked = "blocked";
export const RunDone = "done";
export const RunCancelled = "cancelled";

// Run-specific step statuses (StepRunning/StepDone are shared, in records.ts) + kinds.
export const StepUpcoming = "upcoming";
export const StepWaiting = "waiting";
export const KindWork = "work";
export const KindCheckpoint = "checkpoint";
export const KindBrief = "brief";

export interface RunStep {
  id: string;
  label?: string;
  kind: string;
  status: string;
  agent?: string;
}

export interface RelatesLink {
  goal: string;
  crit?: string;
  kind: string; // "toward" | "proof" | "related"
}

export interface ActivityEntry {
  id: string;
  glyph?: string;
  text: string;
  src?: string;
  at: number;
}

export interface ProducedArtifact {
  name: string;
  kind?: string;
  version?: number;
  status?: string;
}

// Run is the sextant.workflow.run/v1 state envelope. template is `string | null`
// (explicit null = ad-hoc), matching Go's *string pointer.
export interface Run {
  $type?: string;
  id: string;
  template: string | null;
  label?: string;
  objective?: string;
  status: string;
  steps: RunStep[];
  relates: RelatesLink[];
  activity: ActivityEntry[];
  artifacts: ProducedArtifact[];
  stop?: string[];
  created?: number;
  owner?: string;
}

export interface RunEvent {
  $type?: string;
  step?: string;
  status: string;
  by?: string;
  note?: string;
  outcome?: string;
  artifacts?: ProducedArtifact[];
}

export interface RunControl {
  $type?: string;
  verb: string;
}

export interface RunStartRequest {
  $type?: string;
  id: string;
  nonce?: string;
}

export interface RunStartAck {
  $type?: string;
  id?: string;
  nonce?: string;
  requestId: string;
  status: string;
  error?: string;
}

export interface Template {
  $type?: string;
  name: string;
  description?: string;
  steps: { id: string; label?: string; kind: string }[];
  triggers?: JSONValue[];
  stop_conditions?: string[];
}

// isTerminalRun reports whether a run status is final (done/blocked/cancelled).
export function isTerminalRun(status: string): boolean {
  return status === RunDone || status === RunBlocked || status === RunCancelled;
}

// nextPendingRun returns the index of the first step not yet done, or -1 (the peer
// of Go's Run.NextPending).
export function nextPendingRun(r: Run): number {
  for (let i = 0; i < r.steps.length; i++) {
    if (r.steps[i]!.status !== StepDone) return i;
  }
  return -1;
}

export function marshalRun(r: Run): JSONValue {
  return { ...r, $type: KindRun } as unknown as JSONValue;
}

export function parseRun(record: JSONValue): Run | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  if ((record as { [k: string]: JSONValue })["$type"] !== KindRun) return null;
  return record as unknown as Run;
}

export function marshalRunEvent(e: RunEvent): JSONValue {
  return { ...e, $type: TypeRunEvent } as unknown as JSONValue;
}

export function parseRunEvent(record: JSONValue): RunEvent | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  if ((record as { [k: string]: JSONValue })["$type"] !== TypeRunEvent) return null;
  return record as unknown as RunEvent;
}

export function parseRunControl(record: JSONValue): RunControl | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  if ((record as { [k: string]: JSONValue })["$type"] !== TypeRunControl) return null;
  return record as unknown as RunControl;
}

// Convention subjects + state-artifact name for the run contract.
export function runStateName(id: string): string {
  return "workflow.run." + id;
}
export function runEventsSubject(id: string): string {
  return "msg.workflow.run." + id + ".events";
}
export function runControlSubject(id: string): string {
  return "msg.workflow.run." + id + ".control";
}

// RunStartSubject is the well-known subject the coordinator watches for run.start.
export const RunStartSubject = "msg.topic.run.start";

// runStartRecord renders a run.start request as a canonical record payload (the peer
// of Go's RunStartRecord; both emit byte-identical bytes).
export function runStartRecord(req: RunStartRequest): JSONValue {
  return { ...req, $type: TypeRunStart } as unknown as JSONValue;
}

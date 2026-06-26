// The workflow lexicon in TypeScript (ADR-0011, TASK-239): the record types and
// parse/marshal helpers of the workflow convention, a co-equal peer of
// conventions/workflow/go. A workflow is a CONVENTION over the two primitives, run
// by an ordinary coordinator client — no engine in core. The state envelope is a
// regular Artifact (CAS-checkpointed) keyed by id; control/events ride msg.*
// subjects. The browser dash parses these records to render a run's progress.

import type { JSONValue } from "@sextant/sdk";

// $type discriminators and the versioned state-record kind (the peers of Go's
// constants). KindWorkflow is versioned in its $type so the shape evolves without a
// protocol epoch bump (ADR-0011).
export const KindWorkflow = "sextant.workflow/v1";
export const TypeWorkflowEvent = "workflow.event";
export const TypeWorkflowControl = "workflow.control";
export const TypeWorkflowStart = "workflow.start";
export const TypeWorkflowStartAck = "workflow.start.ack";

// Workflow statuses and step statuses (the goal lexicon's enums).
export const WfRunning = "running";
export const WfPaused = "paused";
export const WfDone = "done";
export const WfCancelled = "cancelled";
export const WfFailed = "failed";

export const StepPending = "pending";
export const StepRunning = "running";
export const StepDone = "done";
export const StepFailed = "failed";

export const StatusOK = "ok";
export const StatusError = "error";

// Step is one unit of work. Kind "dispatch" stands up an agent: the coordinator
// publishes a spawn.request and the work is done when the agent reports a step-done
// workflow.event. agent is filled from the spawn.ack once the child is minted.
export interface Step {
  id: string;
  kind: string;
  nickname?: string;
  prompt?: string;
  status: string;
  agent?: string;
}

// Workflow is the sextant.workflow/v1 state envelope: single-writer (the owner),
// CAS-guarded, with steps as a flat status list (no transition logic in the
// substrate — the coordinator walks it). Stored as an Artifact keyed by id.
export interface Workflow {
  $type?: string;
  id: string;
  status: string;
  owner: string;
  steps: Step[];
}

// WorkflowEvent is the free-form history stream alongside the state envelope, on
// msg.workflow.<id>.events.
export interface WorkflowEvent {
  $type?: string;
  step?: string;
  status: string;
  by?: string;
  note?: string;
}

// isTerminal reports whether a workflow status is final (done/cancelled/failed): a
// resumed coordinator does nothing for a workflow already in a terminal state. The
// peer of Go's IsTerminal.
export function isTerminal(status: string): boolean {
  return status === WfDone || status === WfCancelled || status === WfFailed;
}

// nextPending returns the index of the first step that is not yet done (skipping
// done steps — this is what makes a resumed coordinator idempotent), or -1 if all
// steps are done. The peer of Go's Workflow.NextPending.
export function nextPending(w: Workflow): number {
  for (let i = 0; i < w.steps.length; i++) {
    if (w.steps[i]!.status !== StepDone) return i;
  }
  return -1;
}

// marshalWorkflow renders a workflow state envelope, stamping the versioned $type
// (the peer of Go's Workflow.Marshal).
export function marshalWorkflow(w: Workflow): JSONValue {
  return { ...w, $type: KindWorkflow } as unknown as JSONValue;
}

// parseWorkflow decodes a record as a sextant.workflow/v1 state envelope, returning
// null for any other $type (the peer of Go's ParseWorkflow). The browser dash uses
// it to render a run instead of a hand-rolled `rec["$type"] === "sextant.workflow/v1"`
// check.
export function parseWorkflow(record: JSONValue): Workflow | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  const r = record as { [k: string]: JSONValue };
  if (r["$type"] !== KindWorkflow) return null;
  return record as unknown as Workflow;
}

// parseWorkflowEvent decodes a record as a workflow.event (the peer of Go's
// ParseWorkflowEvent).
export function parseWorkflowEvent(record: JSONValue): WorkflowEvent | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  const r = record as { [k: string]: JSONValue };
  if (r["$type"] !== TypeWorkflowEvent) return null;
  return record as unknown as WorkflowEvent;
}

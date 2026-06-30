// The run contract in TypeScript (ADR-0048) — the co-equal peer of
// conventions/workflow/go/run.go. A RUN is one live instance of work (a ULID with
// steps); a TEMPLATE is the reusable spec. Both are conventions over the two
// primitives: the run envelope is a CAS-checkpointed Artifact (single-writer = the
// coordinator), control/events ride msg.* subjects. The browser dash parses the run
// envelope to render progress and emits run.start / run.control over its own client.
//
// The step statuses and ack statuses it reuses are shared from records.ts.

import type { JSONValue } from "@sextant/sdk";
import { StepDone } from "./records.js";

export const KindRun = "sextant.workflow.run/v1";
export const KindTemplate = "sextant.workflow.template/v1";
export const TypeRunEvent = "run.event";
export const TypeRunControl = "run.control";
export const TypeRunStart = "run.start";
export const TypeRunStartAck = "run.start.ack";

// Agent-mode review lexicon (TASK-242). In agent mode the programmatic shell asks a
// long-lived coordinator AGENT to review each completed step (run.review) and applies the
// agent's reply (run.decision). The shell stays the SOLE single-writer of the run
// envelope; the agent only emits a run.decision, never writing the envelope.
export const TypeRunReview = "run.review";
export const TypeRunDecision = "run.decision";

// Agent-mode decision verbs — the FLAT-STEP-MODEL v1 vocabulary (TASK-242). EXACTLY these
// four: no graph reshaping (branch/insert/skip) in v1.
export const DecisionAdvance = "advance";
export const DecisionRedo = "redo-with-feedback";
export const DecisionEdit = "edit-then-advance";
export const DecisionStop = "stop";

// isDecisionVerb reports whether v is one of the four FLAT-STEP-MODEL v1 verbs (the peer
// of Go's IsDecisionVerb). The shell rejects any graph-reshaping verb.
export function isDecisionVerb(v: string): boolean {
  return (
    v === DecisionAdvance || v === DecisionRedo || v === DecisionEdit || v === DecisionStop
  );
}

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
// KindVerify is an independent verification step (D8): a SEPARATE worker (a producer
// cannot verify itself) fetches the run's real deliverable, builds + runs the relevant
// tests, checks each AC adversarially, and reports outcome=blocked (with a verdict
// artifact) if DoD is not met — placed before a brief so a run cannot reach done over a
// failed verification. Additive — existing kinds unchanged.
export const KindVerify = "verify";
// KindPROpen is the trusted-path PR-open step (TASK-260). NOT a dispatched worker: the
// sandboxed pi worker's egress is walled to the model API (github.com denied, TASK-118)
// and it is never given git/gh credentials, so it cannot push or open a PR. The
// coordinator — a host-side Go service with the operator's ambient git/gh auth — runs
// this step itself against the run's isolated worktree (Run.repo + branch sxrun/<runID>):
// it commits the pending changes, pushes the branch to origin (scoped to sxrun/<runID>,
// never a force-push to a shared branch), opens a PR against the run's base, and records
// the PR URL as the step's produced artifact. Placed after verify/brief. Additive.
export const KindPROpen = "pr-open";

export interface RunStep {
  id: string;
  label?: string;
  kind: string;
  status: string;
  agent?: string;
  // model is the optional per-step model declaration (TASK-245). When set, the
  // dispatcher runs this step's worker on this model. Omitted = default applies.
  model?: string;
  // timeout_secs is the optional per-step timeout in whole seconds (TASK-257). When
  // > 0 the coordinator bounds this step's dispatch by it instead of the run-wide
  // --step-timeout default — a coding step runs minutes, past the 90s default, so the
  // budget rides the run definition. Omitted/0 = the coordinator default applies.
  // Seconds (an integer), the peer of Go's RunStep.TimeoutSecs — both serialize the
  // SAME wire bytes.
  timeout_secs?: number;
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
  // agent_mode opts the run into the long-lived coordinator-AGENT review loop (TASK-242).
  // Additive and opt-in; absent/false is the existing programmatic path.
  agent_mode?: boolean;
  // repo is the absolute path to the git repository this run's work happens in
  // (TASK-256). When set, the coordinator provisions one isolated git worktree per run
  // (branch sxrun/<id> off repo_ref, or HEAD when unset), runs every step inside it, and
  // tears it down on terminal. From the run/template definition, never an operator env
  // var. The peer of Go's Run.Repo. Omitted = no provisioning (scratch-default fallback).
  repo?: string;
  // repo_ref is the optional base ref the per-run worktree branches from (branch/tag/
  // commit). Omitted = the repo's current HEAD; ignored when repo is empty.
  repo_ref?: string;
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

// RunReview is the agent-mode review REQUEST the shell publishes when a step completes
// (TASK-242). produced carries the typed refs the step's worker produced, so the agent can
// dereference and READ each (the one sanctioned content read). Peer of Go's RunReview.
export interface RunReview {
  $type?: string;
  step: string;
  objective?: string;
  label?: string;
  produced?: ProducedArtifact[];
}

// RunDecision is the agent's reply the shell applies (TASK-242). verb is one of the four
// FLAT-STEP-MODEL v1 verbs; feedback is threaded into a redo-with-feedback re-dispatch;
// reason is recorded on the activity trail. Peer of Go's RunDecision.
export interface RunDecision {
  $type?: string;
  step: string;
  verb: string;
  feedback?: string;
  reason?: string;
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

export function marshalRunReview(r: RunReview): JSONValue {
  return { ...r, $type: TypeRunReview } as unknown as JSONValue;
}

export function parseRunReview(record: JSONValue): RunReview | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  if ((record as { [k: string]: JSONValue })["$type"] !== TypeRunReview) return null;
  return record as unknown as RunReview;
}

export function marshalRunDecision(d: RunDecision): JSONValue {
  return { ...d, $type: TypeRunDecision } as unknown as JSONValue;
}

export function parseRunDecision(record: JSONValue): RunDecision | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return null;
  if ((record as { [k: string]: JSONValue })["$type"] !== TypeRunDecision) return null;
  return record as unknown as RunDecision;
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
// Agent-mode review subjects (TASK-242). The shell publishes a run.review on .review and
// awaits the agent's run.decision on .decision. Peers of Go's RunReviewSubject/
// RunDecisionSubject — both emit identical subjects.
export function runReviewSubject(id: string): string {
  return "msg.workflow.run." + id + ".review";
}
export function runDecisionSubject(id: string): string {
  return "msg.workflow.run." + id + ".decision";
}
// runTopicSubject is the run's OPERATOR thread: msg.topic.run.<id>. The dash run view
// posts an operator steer here; the coordinator subscribes it and routes the steer to
// the active step's worker (TASK-246). Distinct from the machine channels (.events,
// .control). Peer of Go's RunTopicSubject — both emit the identical subject.
export function runTopicSubject(id: string): string {
  return "msg.topic.run." + id;
}

// RunStartSubject is the well-known subject the coordinator watches for run.start.
export const RunStartSubject = "msg.topic.run.start";

// runStartRecord renders a run.start request as a canonical record payload (the peer
// of Go's RunStartRecord; both emit byte-identical bytes).
export function runStartRecord(req: RunStartRequest): JSONValue {
  return { ...req, $type: TypeRunStart } as unknown as JSONValue;
}

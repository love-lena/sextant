// The goals convention in TypeScript: the single home for goal mechanics
// (ADR-0041, TASK-175), a co-equal peer of conventions/goal/go. A goal
// is a shared objective — a north-star sentence plus the acceptance criteria that
// define "done" — published as the latest-value artifact goal.<id>, with
// transitions announced on msg.topic.goals as goal.update messages (ADR-0035).
// The goal's STATUS is derived from its criteria rollup, never stored.
//
// As an engine-as-a-library (ADR-0011), a verb here translates a domain action
// into the same primitive bus operations a bare client could issue — get,
// compare-and-set, publish. It reaches the bus only through the Ops seam below,
// the structural subset of the SDK's Client a verb needs; the verb LOGIC is
// hand-written (concept, not codegen) while the record TYPES are generated
// (goal_gen.ts). The emitted primitive operations match the Go convention's
// byte-for-byte — that is what the conformance vectors pin (the co-equality proof).

import type { JSONValue } from "@sextant/sdk";
import { topicSubject } from "@sextant/sdk";

// GoalsSubject is the observable stream of goal transitions: msg.topic.goals,
// where every goal.update is published (ADR-0035). The artifact goal.<id> carries
// the current value; this topic carries the events.
export const GoalsSubject = topicSubject("goals");

// artifactName is the artifact a goal's current value lives in: goal.<id>.
export function artifactName(goalId: string): string {
  return "goal." + goalId;
}

// Statuses a criterion may hold (the goal lexicon's enum). StatusMet is invariant
// — it reads as met only with proof, see [criterionMet]; the rest are signalled by
// whoever is doing the work.
export const StatusMet = "met";
export const StatusInProgress = "in-progress";
export const StatusWaitingOnYou = "waiting-on-you";
export const StatusBlocked = "blocked";
export const StatusNotStarted = "not-started";

// Ops is the primitive bus surface the goal verbs are written against — the subset
// of the SDK a verb needs: read an artifact, compare-and-set it, publish a message.
// It is a consumer-defined interface (declared where it is used, kept small), so
// the same verb runs live against the SDK Client and is recorded against the
// conformance Recorder, neither importing the other. The SDK's Client satisfies it
// structurally; the Recorder in the conformance suite implements the same shape.
//
// The peer of Go's goals.Ops: there the record crosses as json.RawMessage; here it
// crosses as a parsed JSONValue (the SDK's natural currency), which the canonical
// rule compares identically.
export interface Ops {
  // getArtifact reads an artifact's current record and revision.
  getArtifact(name: string): Promise<{ record: JSONValue; revision: number }>;
  // updateArtifact compare-and-sets an artifact's record; expectedRev guards a lost
  // update. Returns the new revision.
  updateArtifact(name: string, record: JSONValue, expectedRev: number): Promise<number>;
  // publish issues a message.publish on subject (must be under msg.) with record.
  publish(subject: string, record: JSONValue): Promise<void>;
}

// SetCriterionInput is the domain input to [setCriterion] — the verb's signature,
// mirrored in the goal lexicon's verbs.setCriterion (the contract Go also
// implements). The field names match the lexicon exactly.
export interface SetCriterionInput {
  // goalId identifies the goal; the artifact is goal.<goalId>.
  goalId: string;
  // criterionId is the criterion to set, unique within the goal.
  criterionId: string;
  // status is the new status — one of the Status* constants.
  status: string;
  // headline is a short line describing the transition, for the announcement.
  headline: string;
  // ref is an optional artifact/PR/ticket that triggered the update.
  ref?: string;
  // by is an optional convenience label for who set it (the bus-stamped author of
  // the write is authoritative).
  by?: string;
}

// Update is the goal.update message a transition announces on [GoalsSubject]
// (protocol/lexicons/goal.update.json): an observation that a goal moved, not its
// current value. It signals; it does not manage. The field set + names mirror Go's
// goals.Update exactly so the published record canonicalizes byte-identically.
export interface Update {
  $type: "goal.update";
  goal: string;
  crit?: string;
  status?: string;
  headline: string;
  ref?: string;
  updated?: string;
  by?: string;
}

// SetCriterionError marks WHICH step of [setCriterion] failed, so a caller can
// react precisely — notably a best-effort approve loop, which retries ONLY a
// failed compare-and-set update (a concurrent write moved the revision) and never
// a get or a publish. The `step` field is the discriminant (the TS peer of Go's
// errors.Is sentinels ErrGet/ErrUpdate/ErrPublish).
export type SetCriterionStep = "get" | "rewrite" | "update" | "publish";

export class SetCriterionError extends Error {
  readonly step: SetCriterionStep;
  override readonly cause?: unknown;
  constructor(step: SetCriterionStep, message: string, cause?: unknown) {
    super(message);
    this.name = "SetCriterionError";
    this.step = step;
    this.cause = cause;
  }
}

// setCriterion sets one criterion's status on a goal: it reads goal.<goalId>,
// rewrites that criterion's status in place (every other field preserved),
// compare-and-sets it back, then announces the transition on msg.topic.goals. This
// is the convention's single write path.
//
// It is idempotent: a criterion already at the target status is a no-op — no write,
// no announce — and changed is false. The verb itself does not loop; its recorded
// transcript is a single get→update→publish. A failure is a [SetCriterionError]
// carrying the step (get/rewrite/update/publish) so a best-effort caller retries
// only the CAS update and distinguishes a write that landed but failed to announce.
//
// The criterion must exist; setting an absent criterion is a no-op with
// changed=false (not an error — the caller may be racing a goal edit). A record
// that is not a goal shape is an error (step "rewrite").
//
// `now` is the timestamp the goal.update stamps. The live caller passes the real
// time; the recorded conformance verb passes a fixed time so the transcript is
// byte-stable — exactly as the Go verb takes `now string`.
export async function setCriterion(
  ops: Ops,
  input: SetCriterionInput,
  now: string,
): Promise<boolean> {
  const name = artifactName(input.goalId);

  let record: JSONValue;
  let rev: number;
  try {
    const got = await ops.getArtifact(name);
    record = got.record;
    rev = got.revision;
  } catch (e) {
    throw new SetCriterionError("get", `goals: get goal ${name}: ${errText(e)}`, e);
  }

  let merged: JSONValue;
  let changed: boolean;
  try {
    [merged, changed] = setCriterionStatus(record, input.criterionId, input.status);
  } catch (e) {
    throw new SetCriterionError(
      "rewrite",
      `goals: rewrite criterion ${JSON.stringify(input.criterionId)} on ${name}: ${errText(e)}`,
      e,
    );
  }
  if (!changed) {
    return false;
  }

  try {
    await ops.updateArtifact(name, merged, rev);
  } catch (e) {
    throw new SetCriterionError("update", `goals: update goal ${name}: ${errText(e)}`, e);
  }

  const update = buildUpdate(input, now);
  try {
    await ops.publish(GoalsSubject, update as unknown as JSONValue);
  } catch (e) {
    // The write landed; only the announcement failed. The criterion DID move, so a
    // caller must NOT retry the write (it would no-op) — it may re-announce or
    // surface the miss. Throw the publish-step error so the caller can distinguish.
    throw new SetCriterionError(
      "publish",
      `goals: publish goal.update: ${errText(e)}`,
      e,
    );
  }
  return true;
}

// buildUpdate assembles the goal.update record. Optional fields (crit, status,
// ref, updated, by) are OMITTED when empty, mirroring Go's `omitempty` on
// goals.Update — so the published record carries exactly the keys the Go verb
// emits and canonicalizes byte-identically. headline is always present (it is the
// announcement); $type and goal are always present.
function buildUpdate(input: SetCriterionInput, now: string): Update {
  const u: Update = {
    $type: "goal.update",
    goal: input.goalId,
    headline: input.headline,
  };
  if (input.criterionId) u.crit = input.criterionId;
  if (input.status) u.status = input.status;
  if (input.ref) u.ref = input.ref;
  if (now) u.updated = now;
  if (input.by) u.by = input.by;
  return u;
}

// setCriterionStatus rewrites a goal record with criterion crit set to status,
// preserving every other field (the criterion's own text/owner, sibling criteria,
// the north-star, AND any field a future lexicon adds). It returns [record, false]
// — the record untouched — when the criterion is absent or already at status, so
// the caller can skip the write. A record that is not the expected goal shape
// throws.
//
// Like the Go peer, it rewrites at the structural level rather than round-tripping
// through the generated Goal type, so an unknown field is preserved rather than
// dropped — the write path must never silently lose content the bus owns. It works
// on a deep clone so the caller's input record is not mutated.
function setCriterionStatus(
  record: JSONValue,
  crit: string,
  status: string,
): [JSONValue, boolean] {
  if (record === null || typeof record !== "object" || Array.isArray(record)) {
    throw new Error("goals: record is not a goal object");
  }
  const obj = structuredClone(record) as { [k: string]: JSONValue };
  const criteria = obj["criteria"];
  if (criteria === undefined) {
    // No criteria array: not a goal shape we can rewrite. (The Go peer tolerates a
    // missing criteria array — json.Unmarshal yields an empty slice — and reports
    // no change. Match that: nothing to flip, no error.)
    return [record, false];
  }
  if (!Array.isArray(criteria)) {
    throw new Error("goals: record.criteria is not an array");
  }

  let changed = false;
  for (const c of criteria) {
    if (c === null || typeof c !== "object" || Array.isArray(c)) continue;
    const cm = c as { [k: string]: JSONValue };
    if (cm["id"] !== crit) continue;
    if (cm["status"] === status) {
      // Already at the target status — nothing to do.
      return [record, false];
    }
    cm["status"] = status;
    changed = true;
    break;
  }
  if (!changed) {
    return [record, false];
  }
  return [obj, true];
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

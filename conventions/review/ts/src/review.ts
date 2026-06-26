// The review convention in TypeScript (ADR-0044): an artifact carries a
// review-state as a `review` block inside its record — NOT a change to the core
// artifact primitive (create/get/update/list are untouched). Absent => the UI
// reads the artifact as neutral (draft); a producer sets state="review" explicitly
// when the artifact is for the operator's judgment. This is the convention the
// dash's review surface used to run server-side (clients/sextant-dash/dashapi/
// review.go); it moves here so the browser dash runs it directly over its own bus
// Client (ADR-0044) — read-merge-CAS in TS, plus the approve→met closed loop via
// the goals convention's single write path. No bus feature, no new protocol.
//
// As an engine-as-a-library (ADR-0011), a verb here translates a domain action
// (the operator's verdict) into the same primitive bus operations a bare client
// could issue — get, compare-and-set, publish — reaching the bus only through the
// Ops seam (the structural subset of the SDK's Client it needs). The SDK Client
// satisfies it; a test supplies a fake.

import type { JSONValue } from "@sextant/sdk";
import {
  type Ops as GoalsOps,
  setCriterion,
  SetCriterionError,
  StatusMet,
  proofRelations,
} from "@sextant/conv-goals";

// Ops is the primitive bus surface the review verbs are written against — get,
// compare-and-set, publish — identical to the goals convention's Ops, so the same
// SDK Client (or a fake) satisfies both structurally. Re-declared here (a
// consumer-defined interface) rather than imported so the review convention does
// not couple to the goals seam at the type level.
export interface Ops {
  getArtifact(name: string): Promise<{ record: JSONValue; revision: number }>;
  updateArtifact(name: string, record: JSONValue, expectedRev: number): Promise<number>;
  publish(subject: string, record: JSONValue): Promise<void>;
}

// REVIEW_STATES are the states the convention recognises (the peer of Go's
// reviewStates). A verdict outside this set is rejected so a typo never persists.
export const REVIEW_STATES = ["review", "approved", "changes", "draft", "rejected", "archived"] as const;
export type ReviewState = (typeof REVIEW_STATES)[number];

// isReviewState reports whether s is a recognised review state.
export function isReviewState(s: string): s is ReviewState {
  return (REVIEW_STATES as readonly string[]).includes(s);
}

// ReviewBlock is the convention's record field (the peer of Go's reviewBlock). It
// has two halves: `state` is the producer's needs-your-eyes INTENT (settable by
// anyone, no verdict fields), and `by`/`at`/`rev` are the operator's VERDICT,
// set on approve/changes. The verdict fields are omitted when empty so a
// producer-set, state-only block round-trips without phantom attribution.
export interface ReviewBlock {
  state: string;
  by?: string;
  at?: string;
  rev?: number; // the artifact revision this review was made against
}

// SetReviewInput is the domain input to setReview: the artifact name, the new
// state, and who is making the verdict (the bus-stamped author of the write is
// authoritative; `by` is the convenience label, like the Go endpoint's s.bus.ID()).
export interface SetReviewInput {
  name: string;
  state: ReviewState;
  by: string;
  // now is the verdict timestamp (RFC3339). The live caller passes the real time;
  // a test passes a fixed time so the merged record is stable.
  now: string;
}

// AdvancedCrit reports one (goal, crit) the closed loop advanced to met — the peer
// of Go's advancedCrit. Returned by setReview on an approve so the UI can surface
// "this approval moved goal X criterion Y to met".
export interface AdvancedCrit {
  goal: string;
  crit: string;
}

// SetReviewResult is setReview's outcome: the artifact name, its new revision, the
// persisted state, and the criteria the approve closed-loop advanced (empty unless
// state==="approved").
export interface SetReviewResult {
  name: string;
  revision: number;
  review: string;
  advanced: AdvancedCrit[];
}

// ReviewError marks a failed setReview: a get/update failure (the verdict did not
// persist) or a malformed record. The closed loop is best-effort and never throws
// — its failures are swallowed, the verdict is the primary outcome.
export class ReviewError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ReviewError";
  }
}

// setReview persists an artifact's review-state by merging a `review` block into
// its record (read → merge → compare-and-set), preserving every other top-level
// field. A stale CAS is retried ONCE before reporting a conflict (the peer of the
// Go endpoint's `attempts=2` loop). On an approve it then runs the closed loop
// (best-effort): flip any proof-related goal criteria to met and announce them.
// The verdict write is the primary outcome — a closed-loop hiccup never fails it.
export async function setReview(ops: Ops, input: SetReviewInput): Promise<SetReviewResult> {
  if (!isReviewState(input.state)) {
    throw new ReviewError(`review: state must be one of: ${REVIEW_STATES.join(", ")}`);
  }
  const attempts = 2;
  for (let i = 0; i < attempts; i++) {
    let got: { record: JSONValue; revision: number };
    try {
      got = await ops.getArtifact(input.name);
    } catch (e) {
      throw new ReviewError(`review: artifact ${JSON.stringify(input.name)} not found: ${errText(e)}`);
    }
    let merged: JSONValue;
    try {
      merged = mergeReview(got.record, { state: input.state, by: input.by, at: input.now, rev: got.revision });
    } catch {
      throw new ReviewError("review: artifact record is not a JSON object");
    }
    try {
      const rev = await ops.updateArtifact(input.name, merged, got.revision);
      // The verdict is persisted — the primary outcome. On an approve, run the
      // closed loop (goals-design D3) as a best-effort convenience.
      let advanced: AdvancedCrit[] = [];
      if (input.state === "approved") {
        advanced = await closeLoop(ops, input.name, got.record, input.by, input.now);
      }
      return { name: input.name, revision: rev, review: input.state, advanced };
    } catch (e) {
      if (i === attempts - 1) {
        throw new ReviewError(`review: update failed: ${errText(e)}`);
      }
      // else: a concurrent write moved the revision — re-get and reapply.
    }
  }
  // unreachable (the loop returns or throws), but TypeScript needs a terminus.
  throw new ReviewError("review: update failed");
}

// closeLoop is the approve→met convenience (goals-design D3, the peer of Go's
// closeLoop): for an approved artifact whose record declares proof relations, it
// flips each referenced goal criterion to met and announces it via the goals
// convention's single write path (setCriterion). The dash holds no goal mechanics
// of its own; what counts as a proof relation is goals.proofRelations, the one
// definition both halves share.
//
// It is best-effort: the verdict write has already succeeded, so every error here
// (record without relates, goal.<id> absent, a CAS conflict, a publish error) is
// swallowed — a closed-loop hiccup must never turn the approve into an error. It
// retries each criterion ONCE on a conflict. It returns the (goal, crit) pairs it
// advanced.
export async function closeLoop(
  ops: Ops,
  ref: string,
  record: JSONValue,
  by: string,
  now: string,
): Promise<AdvancedCrit[]> {
  const advanced: AdvancedCrit[] = [];
  const seen = new Set<string>(); // dedup proof relations by (goal, crit)
  for (const rel of proofRelations(record)) {
    const key = rel.goal + "\x00" + rel.crit;
    if (seen.has(key)) continue;
    seen.add(key);
    if (await flipToMet(ops, rel.goal, rel.crit, ref, by, now)) {
      advanced.push({ goal: rel.goal, crit: rel.crit });
    }
  }
  return advanced;
}

// flipToMet sets one goal criterion to met via the goals convention. It returns
// true when the criterion actually moved to met, false when nothing moved (an
// already-met or absent criterion is an idempotent no-op). The caller is
// best-effort, so failures are not propagated; the retry is precise (the peer of
// Go's flipToMet):
//   - the "update" step (a CAS lost a race) is the only retryable failure — re-run
//     setCriterion ONCE, then give up. A get/rewrite failure is not retried.
//   - the "publish" step means the goal write LANDED but the announce didn't. The
//     criterion moved, so this counts as advanced (true); we do not retry.
async function flipToMet(
  ops: Ops,
  goalID: string,
  crit: string,
  ref: string,
  by: string,
  now: string,
): Promise<boolean> {
  const goalsOps = ops as unknown as GoalsOps; // same get/cas/publish shape
  const input = {
    goalId: goalID,
    criterionId: crit,
    status: StatusMet,
    headline: "Criterion met — " + ref + " approved",
    ref,
    by,
  };
  const attempts = 2;
  for (let i = 0; i < attempts; i++) {
    try {
      return await setCriterion(goalsOps, input, now);
    } catch (e) {
      if (e instanceof SetCriterionError) {
        if (e.step === "publish") {
          return true; // the write landed; only the announce missed — it advanced
        }
        if (e.step === "update" && i < attempts - 1) {
          continue; // a concurrent write moved the revision — re-get and reapply once
        }
      }
      return false; // a get/rewrite failure, or the retry is exhausted — give up
    }
  }
  return false;
}

// mergeReview rewrites record with the review block set, preserving every other
// top-level field (the peer of Go's mergeReview). record must be a JSON object
// (documents are); a non-object record throws so the merge never silently drops
// content. Verdict fields (by/at/rev) are omitted when empty so a state-only
// producer block round-trips without phantom attribution.
export function mergeReview(record: JSONValue, rb: ReviewBlock): JSONValue {
  let obj: { [k: string]: JSONValue } = {};
  if (record !== null && record !== undefined) {
    if (typeof record !== "object" || Array.isArray(record)) {
      throw new Error("review: record is not a JSON object");
    }
    obj = { ...(record as { [k: string]: JSONValue }) };
  }
  const block: { [k: string]: JSONValue } = { state: rb.state };
  if (rb.by) block["by"] = rb.by;
  if (rb.at) block["at"] = rb.at;
  if (rb.rev) block["rev"] = rb.rev;
  obj["review"] = block;
  return obj;
}

function errText(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

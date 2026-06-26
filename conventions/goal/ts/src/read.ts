// The read side of the goals convention: parse a goal record, derive its status
// from the criteria rollup, and apply the proof-filter — the met-needs-proof
// invariant. A co-equal peer of conventions/goal/go/read.go: the reading
// rule lives in one place per language and the two cannot disagree about whether a
// goal is done.

import type { JSONValue } from "@sextant/sdk";
import type { Criterion, Goal } from "./goal_gen.js";
import {
  StatusBlocked,
  StatusInProgress,
  StatusMet,
  StatusWaitingOnYou,
} from "./goals.js";

// Relate is one entry of an artifact record's `relates` array — the handle that
// ties a document to a goal criterion. A kind=="proof" relation naming both a goal
// and a crit is what backs a criterion's invariant "met"; a kind=="related" is a
// soft cross-reference that does not.
export interface Relate {
  goal: string;
  crit: string;
  kind: string;
}

// parseRelates reads the `relates` array from any artifact record. A missing or
// malformed relates field yields [] (most artifacts carry no relations) — never an
// error. It is the single parser both the write path (which proof relations close
// a loop) and the read path (which criteria have proof) share.
export function parseRelates(record: JSONValue): Relate[] {
  if (record === null || typeof record !== "object" || Array.isArray(record)) {
    return [];
  }
  const raw = (record as { [k: string]: JSONValue })["relates"];
  if (!Array.isArray(raw)) {
    return [];
  }
  const out: Relate[] = [];
  for (const r of raw) {
    if (r === null || typeof r !== "object" || Array.isArray(r)) continue;
    const m = r as { [k: string]: JSONValue };
    out.push({
      goal: typeof m["goal"] === "string" ? m["goal"] : "",
      crit: typeof m["crit"] === "string" ? m["crit"] : "",
      kind: typeof m["kind"] === "string" ? m["kind"] : "",
    });
  }
  return out;
}

// isProof reports whether this relation backs a specific goal criterion as proof:
// kind=="proof" naming BOTH a goal and a crit. This is the SINGLE definition of
// "what counts as proof" (the Go peer Relate.IsProof). Every proof check goes
// through here.
export function isProof(r: Relate): boolean {
  return r.kind === "proof" && r.goal !== "" && r.crit !== "";
}

// proofs filters already-parsed relations to the proof ones (see [isProof]).
export function proofs(rels: Relate[]): Relate[] {
  return rels.filter(isProof);
}

// proofRelations returns the proof relations among a record's relates — the ones
// that can back a met criterion. It parses the record then applies [isProof].
export function proofRelations(record: JSONValue): Relate[] {
  return proofs(parseRelates(record));
}

// parseGoal decodes a goal record into the generated Goal type. The result is null
// when the record is not a goal — it has no criteria array (the same recognition
// the Go peer uses: a goal is the artifact that carries criteria).
export function parseGoal(record: JSONValue): Goal | null {
  if (record === null || typeof record !== "object" || Array.isArray(record)) {
    return null;
  }
  const obj = record as { [k: string]: JSONValue };
  if (obj["criteria"] === undefined) {
    return null;
  }
  if (!Array.isArray(obj["criteria"])) {
    return null;
  }
  const criteria: Criterion[] = [];
  for (const c of obj["criteria"]) {
    if (c === null || typeof c !== "object" || Array.isArray(c)) {
      return null;
    }
    const cm = c as { [k: string]: JSONValue };
    criteria.push({
      id: typeof cm["id"] === "string" ? cm["id"] : "",
      text: typeof cm["text"] === "string" ? cm["text"] : "",
      status: typeof cm["status"] === "string" ? cm["status"] : "",
      ...(typeof cm["owner"] === "string" ? { owner: cm["owner"] } : {}),
    });
  }
  const goal: Goal = {
    northstar: typeof obj["northstar"] === "string" ? obj["northstar"] : "",
    criteria,
  };
  if (typeof obj["stream"] === "string") goal.stream = obj["stream"];
  if (typeof obj["updated"] === "string") goal.updated = obj["updated"];
  if (typeof obj["by"] === "string") goal.by = obj["by"];
  return goal;
}

// criterionMet reports whether criterion c reads as met, applying the proof
// invariant: a criterion is met only when its stored status is "met" AND at least
// one proof-kind artifact backs it (provedCrits has c.id). A stored "met" with no
// proof reads as in-progress. provedCrits is the set of criterion ids the caller
// found a proof artifact for (see [provedCriteria]).
//
// This is the proof-filter, in one place — the read-side arbiter of the lexicon's
// "met (satisfied; invariant — has >=1 proof-kind artifact in relates)".
export function criterionMet(c: Criterion, provedCrits: Set<string>): boolean {
  return c.status === StatusMet && provedCrits.has(c.id);
}

// effectiveStatus returns a criterion's status as it should READ, applying the
// proof-filter: a stored "met" without proof reads as "in-progress"; every other
// status reads as stored. It is what a UI or digest should display.
export function effectiveStatus(c: Criterion, provedCrits: Set<string>): string {
  if (c.status === StatusMet && !provedCrits.has(c.id)) {
    return StatusInProgress;
  }
  return c.status;
}

// provedCriteria builds the proved-criteria set for goal goalId from a set of
// artifact records (the artifacts directory). A criterion id is in the set when
// some artifact's relates carries a proof relation naming this goal and that
// criterion.
export function provedCriteria(goalId: string, records: JSONValue[]): Set<string> {
  const proved = new Set<string>();
  for (const rec of records) {
    for (const p of proofRelations(rec)) {
      if (p.goal === goalId) {
        proved.add(p.crit);
      }
    }
  }
  return proved;
}

// Rollup is the derived view of a goal's progress: how many criteria are met
// (after the proof-filter) of the total, and the salient flags a front-end groups
// on. Goal status is DERIVED — there is no stored goal-status field. Mirrors the Go
// peer's Rollup struct.
export interface Rollup {
  met: number; // criteria that read as met (status met AND proved)
  total: number; // total criteria
  waiting: number; // criteria waiting on the operator
  blocked: boolean; // any criterion hard-blocked
  defined: boolean; // has a north-star and at least one criterion
}

// rollup derives goal g's progress, applying the proof-filter via provedCrits. A
// criterion's "met" only counts when proved; an unproved stored "met" counts as
// in-progress (neither met nor waiting nor blocked). defined is false for a goal
// with no north-star or no criteria.
export function rollup(g: Goal, provedCrits: Set<string>): Rollup {
  const r: Rollup = {
    met: 0,
    total: g.criteria.length,
    waiting: 0,
    blocked: false,
    defined: g.northstar !== "" && g.criteria.length > 0,
  };
  for (const c of g.criteria) {
    switch (effectiveStatus(c, provedCrits)) {
      case StatusMet:
        r.met++;
        break;
      case StatusWaitingOnYou:
        r.waiting++;
        break;
      case StatusBlocked:
        r.blocked = true;
        break;
    }
  }
  return r;
}

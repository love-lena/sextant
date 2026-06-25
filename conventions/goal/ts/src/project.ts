// The Goals PROJECTION in TypeScript — a co-equal peer of
// conventions/goal/go/project.go (ADR-0041, ADR-0044). It is the
// read-model a UI renders, built ONCE so the proof-filter rule (a criterion reads
// met only with proof) and the evidence wiring live in ONE place per language,
// never reimplemented per client. The browser dash consumes Project directly over
// its own bus Client (ADR-0044), replacing the Go-backend /api/goals projection;
// the dash JS is then a dumb renderer of effective statuses, not a second copy of
// the proof rule. The served shape is byte-compatible with the Go GoalView so the
// SPA reads one shape regardless of which peer produced it.
//
// A projection is derived from the artifact directory: the goal.<id> records plus
// every other record (so proof relations pointing at a goal are found). It is pure
// — no bus, no IO — so the caller lists once and hands the records in.

import type { JSONValue } from "@sextant/sdk";
import type { Criterion } from "./goal_gen.js";
import {
  parseGoal,
  parseRelates,
  effectiveStatus,
  rollup,
  type Rollup,
} from "./read.js";

// Artifact is one entry of the artifact directory the projection is built from:
// the artifact's name, its record, and the bus-stamped revision. The caller
// projects the SDK's richer ArtifactInfo/Artifact down to this. Mirrors Go's
// goals.Artifact.
export interface Artifact {
  name: string;
  record: JSONValue;
  revision: number;
}

// Evidence is one artifact backing a criterion (or a goal): its name and whether
// it is proof (kind=="proof") or a softer related reference. A criterion reads met
// only when it has >=1 proof Evidence — the invariant effectiveStatus enforces.
// Mirrors Go's goals.Evidence (the JSON field names match: name, kind).
export interface Evidence {
  name: string;
  kind: string; // "proof" | "related"
}

// CriterionView is a criterion as a UI renders it: its identity and text, its
// EFFECTIVE status (proof-filter already applied), the owner, and the evidence
// backing it. Mirrors Go's goals.CriterionView; the JSON field names match.
export interface CriterionView {
  id: string;
  text: string;
  status: string; // effective status (post proof-filter)
  owner?: string;
  evidence?: Evidence[];
}

// GoalView is a goal as a UI renders it: identity, north-star, stream, the
// criteria with effective statuses + evidence, the derived rollup, the optional
// review-state, and bus-stamped revision. Mirrors Go's goals.GoalView; the JSON
// field names match so the dash SPA reads one shape across the Go and TS peers.
export interface GoalView {
  id: string;
  name: string; // the artifact name, goal.<id>
  northstar: string;
  stream?: string;
  updated?: string;
  by?: string;
  revision: number;
  review?: string; // review-state from the goal record (sign-off convention)
  criteria: CriterionView[];
  evidence?: Evidence[]; // goal-level relations (no crit)
  rollup: Rollup;
}

// EvidenceIndex maps a goal id to the evidence backing it: per-criterion and
// goal-level (a relation with a goal but no crit). Built once from the whole
// directory, the inverse of the relates array that points FROM an artifact AT a
// goal/criterion. Mirrors Go's evidenceIndex.
interface EvidenceIndex {
  crit: Map<string, Map<string, Evidence[]>>; // goalID -> critID -> evidence
  goal: Map<string, Evidence[]>; // goalID -> goal-level evidence
}

function indexEvidence(arts: Artifact[]): EvidenceIndex {
  const idx: EvidenceIndex = { crit: new Map(), goal: new Map() };
  for (const a of arts) {
    for (const rel of parseRelates(a.record)) {
      if (rel.goal === "") continue;
      const kind = rel.kind === "proof" ? "proof" : "related";
      const ev: Evidence = { name: a.name, kind };
      if (rel.crit !== "") {
        let byCrit = idx.crit.get(rel.goal);
        if (!byCrit) {
          byCrit = new Map();
          idx.crit.set(rel.goal, byCrit);
        }
        const list = byCrit.get(rel.crit) ?? [];
        list.push(ev);
        byCrit.set(rel.crit, list);
      } else {
        const list = idx.goal.get(rel.goal) ?? [];
        list.push(ev);
        idx.goal.set(rel.goal, list);
      }
    }
  }
  return idx;
}

// provedFrom returns the proved-criteria set for goalID from the evidence index: a
// criterion is proved when it has >=1 proof-kind evidence. Same invariant
// provedCriteria computes from raw records; the projection reuses the index rather
// than re-scanning. Mirrors Go's evidenceIndex.provedFrom.
function provedFrom(idx: EvidenceIndex, goalID: string): Set<string> {
  const proved = new Set<string>();
  const byCrit = idx.crit.get(goalID);
  if (!byCrit) return proved;
  for (const [crit, evs] of byCrit) {
    if (evs.some((e) => e.kind === "proof")) proved.add(crit);
  }
  return proved;
}

// reviewState reads the review-state convention block (an artifact's `review`
// object with a `state` field) off a goal record, for the sign-off affordance a
// UI shows. Absent/malformed => "" (neutral). The goals convention does not own
// review-state, but the goal projection carries it so the UI reads one shape.
// Mirrors Go's reviewState.
function reviewState(record: JSONValue): string {
  if (record === null || typeof record !== "object" || Array.isArray(record)) return "";
  const review = (record as { [k: string]: JSONValue })["review"];
  if (review === null || typeof review !== "object" || Array.isArray(review)) return "";
  const state = (review as { [k: string]: JSONValue })["state"];
  return typeof state === "string" ? state : "";
}

// Project builds the Goals read-model from the artifact directory: one GoalView per
// goal.<id> record, each with the proof-filter applied (effective statuses), the
// derived rollup, evidence wired in, and the review-state read off the goal record.
// The views are sorted by name for stable rendering. A non-goal artifact is ignored
// (it may still be a proof source). This is THE place the proof rule turns the
// stored goal into what a UI shows — the co-equal peer of Go's goals.Project,
// pinned to it by the conformance vectors.
export function project(arts: Artifact[]): GoalView[] {
  const idx = indexEvidence(arts);
  const views: GoalView[] = [];
  for (const a of arts) {
    const g = parseGoal(a.record);
    if (g === null) continue;
    const id = a.name.startsWith("goal.") ? a.name.slice("goal.".length) : a.name;
    const proved = provedFrom(idx, id);
    const view: GoalView = {
      id,
      name: a.name,
      northstar: g.northstar,
      revision: a.revision,
      rollup: rollup(g, proved),
      criteria: [],
    };
    if (g.stream !== undefined) view.stream = g.stream;
    if (g.updated !== undefined) view.updated = g.updated;
    if (g.by !== undefined) view.by = g.by;
    const rev = reviewState(a.record);
    if (rev !== "") view.review = rev;
    const goalEv = idx.goal.get(id);
    if (goalEv && goalEv.length > 0) view.evidence = goalEv;
    for (const c of g.criteria) {
      const cv: CriterionView = {
        id: c.id,
        text: c.text,
        status: effectiveStatus(c as Criterion, proved),
      };
      if (c.owner !== undefined) cv.owner = c.owner;
      const critEv = idx.crit.get(id)?.get(c.id);
      if (critEv && critEv.length > 0) cv.evidence = critEv;
      view.criteria.push(cv);
    }
    views.push(view);
  }
  views.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  return views;
}

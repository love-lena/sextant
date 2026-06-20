// @sextant/conv-goals — the goals convention in TypeScript, a co-equal peer of
// clients/go/conventions/goals (ADR-0041, TASK-175). A goal is a shared objective
// (a north-star plus its acceptance criteria), published as the latest-value
// artifact goal.<id>, with transitions announced on msg.topic.goals as goal.update
// messages (ADR-0035). The goal's status is DERIVED from the criteria rollup, never
// stored.
//
// A convention is a library over the SDK (ADR-0041), never a bus feature. The verb
// LOGIC here is hand-written (concept, not codegen); only the record TYPES are
// generated from the lexicon (goal_gen.ts). The emitted primitive operations match
// the Go convention byte-for-byte — pinned by the conformance vectors under
// protocol/conformance/vectors/goals, the SAME files the Go suite replays.

// The write verb + its seam, input/output shapes, status constants, errors.
export {
  type Ops,
  type SetCriterionInput,
  type Update,
  type SetCriterionStep,
  SetCriterionError,
  setCriterion,
  GoalsSubject,
  artifactName,
  StatusMet,
  StatusInProgress,
  StatusWaitingOnYou,
  StatusBlocked,
  StatusNotStarted,
} from "./goals.js";

// The read side: parse, the proof-filter, the derived rollup.
export {
  type Relate,
  type Rollup,
  parseRelates,
  isProof,
  proofs,
  proofRelations,
  parseGoal,
  criterionMet,
  effectiveStatus,
  provedCriteria,
  rollup,
} from "./read.js";

// Generated record types (from protocol/lexicons/goal.json — DO NOT hand-edit).
export type { Goal, Criterion } from "./goal_gen.js";

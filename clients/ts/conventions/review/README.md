# @sextant/conv-review

The **review convention** in TypeScript (ADR-0044): an artifact carries a
review-state as a `review` block inside its record — not a change to the core
artifact primitive. Absent ⇒ neutral (draft); a producer sets `state: "review"`
explicitly when the artifact is for the operator's judgment.

This is the logic the dash's review surface used to run server-side
(`clients/go/apps/internal/dashapi/review.go`). It moves into a convention library
so the **browser dash runs it directly** over its own bus `Client` — read-merge-CAS
in the browser, plus the approve→met closed loop via `@sextant/conv-goals` — with
no Go-backend re-implementation.

## API

- `setReview(ops, { name, state, by, now })` — read the artifact, merge the
  `review` block (preserving every other field), compare-and-set it (retry once on
  a conflict). On `state === "approved"` it then runs the closed loop. Returns
  `{ name, revision, review, advanced }`.
- `closeLoop(ops, ref, record, by, now)` — for an approved artifact whose record
  declares proof relations, flip each referenced goal criterion to `met` via the
  goals convention's single write path and announce it. Best-effort: a failure
  never fails the verdict. Returns the advanced `(goal, crit)` pairs.
- `mergeReview(record, block)` / `REVIEW_STATES` / `isReviewState` — the building
  blocks.

A verb reaches the bus only through the `Ops` seam (`getArtifact` /
`updateArtifact` / `publish`) — the structural subset of `@sextant/sdk`'s `Client`,
which it satisfies. A test supplies a fake.

## Co-equality

A Go peer (`clients/go/conventions/review`) is a follow-up to complete the
ADR-0041 co-equality (the goals convention has both peers); this TS convention is
the live home for now. The proof-relation rule it relies on is
`@sextant/conv-goals`'s `proofRelations` — the one definition both halves share.

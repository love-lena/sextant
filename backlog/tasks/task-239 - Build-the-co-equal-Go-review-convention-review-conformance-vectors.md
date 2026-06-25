---
id: TASK-239
title: Build the co-equal Go review convention + review conformance vectors
status: To Do
assignee: []
created_date: '2026-06-25 19:41'
labels:
  - ready-for-agent
dependencies:
  - TASK-224
priority: medium
ordinal: 227000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Problem Statement

Review-state is just metadata on an artifact, and any client — agents, not only the browser — will want to get/set it. But the review convention (the operator verdict: setReview + close-loop) exists only in TypeScript (clients/ts/conventions/review), and there are NO review conformance vectors, so review behaviour is not locked across languages. Go has no review convention at all. As headless clients start performing or reacting to review verdicts, the Go gap becomes real and the cross-language behaviour can silently diverge.

## Solution

Make review a proper co-equal convention. Build conventions/review/go as a peer of conventions/review/ts over the same consumer-declared Ops seam (mirroring goals): read review state, setReview (merge the review block, CAS), and closeLoop (on approve, flip proof-related criteria to met). Author review conformance vectors and have both SDKs replay them plus a live cross-language test — exactly the goals pattern.

## User Stories

1. As a headless agent, I want to read an artifact's review state from a Go client, so that I can react to operator verdicts without a browser.
2. As a Go client, I want to set/merge an artifact's review block over a convention, so that the verdict + close-loop behaves identically to the browser.
3. As a maintainer, I want review conformance vectors replayed by both Go and TS, so that the two implementations can never drift in wire behaviour.
4. As a maintainer, I want conventions/review/go to import only protocol (+ its Ops seam), so that it stays a library a bare client could be, never reaching bus internals.

## Implementation Decisions

- Lands AFTER the ADR-0049 restructure (TASK-224), which moves TS review to conventions/review/ts and reserves the conventions/review/go slot. Depends on TASK-224.
- Go review mirrors the TS surface: setReview, mergeReview (preserve unknown top-level fields), closeLoop (dedup proof relations, best-effort on conflict/not-found/publish-miss), read + isReviewState — over the consumer-declared Ops seam (getArtifact / updateArtifact CAS / publish). importcheck-enforced.
- The producer-intent half (setting review.state=\"review\" on an artifact) stays a one-field write any client does directly; it is NOT part of this convention's required surface. This ticket is the operator verdict + close-loop + read model.
- New review conformance vectors under protocol/conformance/vectors/review; both SDKs replay; add a live cross-language coequality test mirroring goals' coequality.test.ts.
- TS review logic is the reference; goals (clients/{go,ts}/conventions/goals) is the structural template.

## Testing Decisions

- A new review conformance vector set IS the intended new seam for this ticket (unlike TASK-224, which adds none) — recorded once, replayed by Go and TS so behaviour is provably identical.
- Prior art: conventions/goals — goals.go/goals.ts, read, project, conformance_test.go, coequality.test.ts.
- Behaviour parity: Go setReview/closeLoop must emit byte-identical operations to TS for the same record; the vectors enforce it.

## Out of Scope

- The ADR-0049 restructure itself (TASK-224).
- Any new operator-verdict UX or new bus operation; this is the convention library + vectors, consumed by existing and future clients.
- Related goals/review dash bugs filed under #266 (e.g. TASK-227/229/232) — separate.

## Further Notes

Decision made in-session 2026-06-25: review becomes a proper co-equal, non-core convention rather than a TS-only corner, because review-state is artifact metadata many clients will get/set. Two-PR split: TASK-224 relocates the TS side + reserves the Go slot; this ticket builds the Go side + vectors so the restructure PR stays purely behaviour-preserving (its green gate is its review).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 conventions/review/go exists, co-equal in shape with conventions/review/ts: setReview, mergeReview, closeLoop, read + isReviewState over the Ops seam
- [ ] #2 review conformance vectors authored under protocol/conformance/vectors/review; Go and TS both replay them green
- [ ] #3 a live cross-language coequality test passes (Go writes / TS reads the same artifact and vice versa; canonical bytes identical), mirroring goals
- [ ] #4 importcheck: conventions/review/go imports only protocol (+ its Ops seam), never the bus
- [ ] #5 no behaviour change to the browser review path (TS review unchanged except its new home from TASK-224)
- [ ] #6 depends on TASK-224 landing first
<!-- AC:END -->

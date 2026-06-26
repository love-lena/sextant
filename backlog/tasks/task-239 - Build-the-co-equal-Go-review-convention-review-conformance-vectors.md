---
id: TASK-239
title: >-
  Full convention coverage across Go and TS: review (Go), workflow (TS), spawn
  (TS) + vectors
status: Done
assignee: []
created_date: '2026-06-25 19:41'
updated_date: '2026-06-26 20:11'
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

Conventions should be co-equal across Go and TS, but coverage is lopsided after the ADR-0049 restructure: goal is co-equal (go+ts, vectors); review is TS-only (no Go, no vectors); workflow and spawn are Go-only (the records.go contracts, no TS, no vectors) even though the dash creates, edits, and renders workflows (and spawns runs). The restructure (TASK-224) relocates each existing side into conventions/<name>/{go,ts} and reserves the empty peer slots; this task fills them so every convention is a co-equal peer with conformance vectors binding behaviour.

## Solution

Bring every convention to go+ts parity, vectors-bound, using goals as the structural template and the existing side as the behavioural reference:
- conventions/review/go — mirror TS review (setReview, mergeReview, closeLoop, read + isReviewState) over the Ops seam.
- conventions/workflow/ts — mirror the Go workflow convention so the dash drives workflows (create/edit/render) over a real convention instead of ad-hoc writes.
- conventions/spawn/ts — mirror the Go spawn convention.
- Author conformance vectors for review, workflow, and spawn; both SDKs replay each set plus a live cross-language coequality test (the goals pattern).

## User Stories

1. As a headless Go agent, I want to read/set an artifact's review state over a convention, so that verdict + close-loop behave identically to the browser.
2. As the dash, I want to create/edit/render workflows over a TS workflow convention, so that workflow writes go through the convention instead of ad-hoc dash code.
3. As the dash, I want to drive spawn over a TS spawn convention, so that spawn-request writes are co-equal with the Go side.
4. As a maintainer, I want review/workflow/spawn conformance vectors replayed by both Go and TS, so that no convention can drift in wire behaviour.
5. As a maintainer, I want every convention to import only protocol (+ its Ops seam), so that they stay libraries a bare client could be, never reaching bus internals.

## Implementation Decisions

- Depends on TASK-224 (the restructure relocates the existing sides and reserves the peer slots: review/go, workflow/ts, spawn/ts).
- goals (clients/{go,ts}/conventions/goals -> conventions/goal/{go,ts}) is the structural template; the existing side of each convention is the behavioural reference for its missing peer.
- Each convention stays a library over the consumer-declared Ops seam (getArtifact / updateArtifact CAS / publish); importcheck-enforced (imports only protocol + Ops, never the bus).
- Producer-intent one-field writes (e.g. setting review.state) remain direct writes, not convention surface.

## Testing Decisions

- New conformance vector sets for review, workflow, and spawn — recorded once, replayed by Go and TS; these are the intended NEW seams for this task (TASK-224 adds none).
- Prior art: conventions/goals — conformance_test.go (Go), coequality.test.ts (TS).
- Behaviour parity: each new peer must emit byte-identical operations to its reference side for the same record; the vectors enforce it.

## Out of Scope

- The ADR-0049 restructure itself (TASK-224).
- Any new operator UX or new bus operation.
- The goals/review dash bugs filed under #266 (e.g. TASK-227/229/232) — separate.

## Further Notes

Scope broadened in-session 2026-06-25 from "Go review only" to full go<->ts convention coverage: the dash renders/creates/edits workflows (and spawns), so workflow and spawn deserve TS peers too. Kept out of TASK-224 so the restructure stays purely behaviour-preserving (its green gate is its review).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 conventions/review/go exists, co-equal in shape with conventions/review/ts: setReview, mergeReview, closeLoop, read + isReviewState over the Ops seam
- [ ] #2 review conformance vectors authored under protocol/conformance/vectors/review; Go and TS both replay them green
- [ ] #3 a live cross-language coequality test passes (Go writes / TS reads the same artifact and vice versa; canonical bytes identical), mirroring goals
- [ ] #4 importcheck: conventions/review/go imports only protocol (+ its Ops seam), never the bus
- [ ] #5 no behaviour change to the browser review path (TS review unchanged except its new home from TASK-224)
- [ ] #6 depends on TASK-224 landing first
- [ ] #7 conventions/workflow/ts built co-equal with conventions/workflow/go, vectors-bound; the dash creates/edits/renders workflows over it (replacing ad-hoc dash writes)
- [ ] #8 conventions/spawn/ts built co-equal with conventions/spawn/go, vectors-bound
- [ ] #9 after this task every convention (goal, review, workflow, spawn) is co-equal go+ts, each with conformance vectors both SDKs replay green
- [ ] #10 The 4 M5-era demos that build the retired clients/go/apps/spawn-poc (docs/demos/{agentic-dev-workflow,m5-workflow-demo,m5-dispatcher-demo,spawn-spike-demo}.sh) are re-pointed at the dispatcher (the client that graduated spawn-poc) or retired; none reference spawn-poc. Deferred out of TASK-224 because re-pointing is a behavioral demo change.
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
All four conventions (goal, review, workflow, spawn) are now co-equal go+ts, each bound by conformance vectors both SDKs replay green. Added conventions/review/go (peer of review/ts: SetReview read-merge-CAS + approve->met closed loop over the Ops seam) with two op-transcript vectors and a live cross-language coequality test; added @sextant/conv-workflow and @sextant/conv-spawn (TS peers) plus a thin write verb (requestWorkflowStart / requestSpawn) on both languages so the records-only conventions have a replayable transcript; recorded vectors under protocol/conformance/vectors/{review,workflow,spawn}. The browser dash now builds workflow.start + spawn.request and renders workflows over the conventions (window.SextantBus) instead of hand-rolled literals (transport unchanged; verified byte-identical; review path untouched). importcheck pins each convention to SDK+protocol(+goals for review), never the bus. m5-workflow-demo re-pointed off the deleted spawn-poc (8/8); three wake-loop demos retired (ADR-0045) with stubs; TASK-240 filed to rebuild them. Full gate green (go build/vet, go test -race, make lint 0 issues, e2e, TS conformance 5/5, review coequality on a real bus) and all CI jobs pass. Shipped on PR #269.
<!-- SECTION:FINAL_SUMMARY:END -->

---
id: TASK-237
title: >-
  Unify artifact & brief review into one surface — every artifact gets the full
  five-verb response set
status: To Do
assignee: []
created_date: '2026-06-25 19:21'
updated_date: '2026-06-25 19:26'
labels:
  - feature
  - dash
  - review
  - artifacts
  - ready-for-agent
  - 'slug:feat-unify-artifact-brief-review-five-verbs'
  - P2
dependencies: []
priority: medium
ordinal: 225000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Today the dash has TWO review surfaces with different response sets: the artifact review (review.jsx, the old 'flow2 DocumentView') offers only Comment / Approve / Request changes, while the brief reader (review-author.jsx, TASK-208) offers the full five verbs. The current design (claude.ai/design project a879e5e0-7130-4a48-bc63-c65cfc9502ad, sx-overlays.jsx BriefReader) has a SINGLE document-review surface with five verbs — there is no separate approve/changes artifact surface; the 3-verb one is a leftover from the older flow2 design the shipped dash carried over. Decision (Lena, 2026-06-25): briefs are just a type of artifact; do NOT make them functionally different — they should be the same thing in the code, and ALL artifacts get all five verbs. So: unify the two surfaces into one review component, route every reviewable artifact through it, and retire the 3-verb path. The five verbs (design, sx-overlays.jsx BriefReader VERBS): Approve · Request revisions · Request answers · Reject · Ignore. Review-state mapping reuses the brief reader's EXISTING persistence (no new contract needed — briefs already do this): approve -> review.state approved; request revisions -> changes; request answers -> changes (distinguished by the companion-topic review message / question marker); reject -> rejected; ignore -> dismiss (no verdict state change). The verbs are richer than the 4 persisted review.state values (CONTEXT.md: review + approved/changes/rejected/archived); the revise/answers/ignore distinction rides the companion-topic message exactly as the brief reader already emits it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 One review component renders every reviewable artifact (brief or otherwise); the separate 3-verb flow2 DocumentView path (review.jsx Comment/Approve/Request changes) is removed
- [ ] #2 Every artifact under review exposes all five verbs — Approve, Request revisions, Request answers, Reject, Ignore — matching the design (sx-overlays.jsx BriefReader) one-for-one in label and order
- [ ] #3 Submitting any verb persists via the existing review path (review.state for approve/revise/answers/reject; ignore dismisses) and posts the durable companion-topic review message; verified after reload (re-read from the bus, not local state)
- [ ] #4 No new review.state enum value is introduced unless required; if one is (e.g. to distinguish 'answers'), it is a canon change (CONTEXT.md / ADR-0034) gated by sign-off — flag, do not silently add
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Merge review.jsx (artifact DocumentView) and review-author.jsx (brief reader) into one component — generalize the richer brief reader to any artifact. Route the dash's artifact-open + brief-open through it; drop the 3-verb VOPTS path. Reuse apiReview + companion-topic emission already wired for briefs.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Requested by Lena 2026-06-25; design pulled fresh from claude.ai/design a879e5e0-7130-4a48-bc63-c65cfc9502ad (sx-overlays.jsx BriefReader = the unified 5-verb surface; shipped review.jsx 3-verb is a flow2 leftover). Decision: briefs = a type of artifact, not functionally different; one component, all 5 verbs. Relates [[task-208]] (the 5-verb brief reader to generalize), [[task-145]] (review.state intent+verdict), [[task-112]] (needs-review explicit). SEPARATE & still-cut: the design's §13 Quick decision (sx-overlays.jsx QuickDecision) is an options-based micro-decision mode (no document), not a verb set — not built (TASK-208 noted §13 cut); track separately if wanted.

2026-06-25: Quick-decision (§13) is now KILLED — wontfix, see TASK-238. Do not build it; do not treat its absence as a design divergence.
<!-- SECTION:NOTES:END -->

---
id: TASK-145
title: 'Tighten ADR-0034/CONTEXT: review.state holds intent AND verdict values'
status: Done
assignee: []
created_date: '2026-06-17 00:03'
updated_date: '2026-06-17 00:33'
labels:
  - documentation
  - convention
  - dash
  - 'slug:doc-review-state-intent-and-verdict'
  - P3
  - ready-for-agent
dependencies: []
ordinal: 135000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The #161 note in CONTEXT.md/ADR-0034 documents review.state's INTENT value (review) but not that the SAME single field also holds the operator VERDICT values (approved/changes/rejected/archived), discriminated by by/at/rev presence (intent has none; a verdict does). That gap caused a live convention round-trip on 2026-06-16. NOT drift: 'changes' is a legit verdict state; #161 only changed the inbox PROJECTION (keys on state==review). SOURCE OF TRUTH: the corrected drop-in canon text is in the convention artifact needs-review-convention rev 960 (canopus) — copy that framing verbatim. Sirius gates this (canon). Standalone tiny doc PR, or fold into the next gated canon-touching dash PR (e.g. Track 2 goals).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 CONTEXT.md + ADR-0034 state: review.state holds the producer INTENT (review, no by/at/rev) AND the operator VERDICTS (approved/changes/rejected/archived, via /review with by/at/rev), discriminated by by/at/rev presence
- [ ] #2 Text copied from needs-review-convention rev 960; lands via a gated doc PR (sirius), folds into the v0.5->main sign-off
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
PR #170 up (gated, sirius's gate); base v0.5, folds into v0.5->main sign-off. Text from needs-review-convention rev 960.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Done in PR #170 (a59f7f3, gated by sirius, merged to v0.5): added the 'review.state is ONE enum (producer intent review + operator verdicts approved/changes/rejected/archived, discriminated by by/at/rev presence)' clarification to the CONTEXT.md needs-review note + the ADR-0034 TASK-112 extension note, plus the changes->'waiting for author' display-label note. Text transcribed from convention artifact needs-review-convention rev 960. Docs-only, no behavior change; folds into the v0.5->main sign-off.
<!-- SECTION:FINAL_SUMMARY:END -->

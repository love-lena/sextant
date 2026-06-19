---
id: TASK-157
title: Review-flagged goals don't surface in the needs-you / review queue
status: Done
assignee: []
created_date: '2026-06-17 20:47'
updated_date: '2026-06-17 21:42'
labels:
  - feature
  - dash
  - goals
  - review-queue
  - P2
  - needs-triage
  - 'slug:feat-review-flagged-goals-surface-needs-you'
dependencies: []
priority: medium
ordinal: 147000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
vega (2026-06-17): a goal with review.state=review (e.g. goal.v0-6-0, flagged for Lena's AC sign-off) does NOT surface in the dash review queue / Home 'needs you'. Goals render only in the Goals view, not projected into the review inbox — so the operator gets no signal a goal needs their sign-off ('how do I know you need my input?'). A goal awaiting operator sign-off should surface like any review-flagged artifact (needs-you projection + review queue), routing to the goal.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A goal with review.state=review appears in the operator's needs-you / review queue (like a review-flagged artifact), linking to the goal
- [x] #2 Clearing the goal's review (the sign-off/verdict) removes it from the queue
- [x] #3 Consistent with the artifact review-queue projection
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered: Lena couldn't tell goal.v0-6-0 needed her AC sign-off (vega 2026-06-17). v0.5.1 dash, not tag-blocking. Relates: [[goals-design]], goals UI #175, TASK-154 (goals discussion topics). violet's curation should also surface it once live.

Built on branch v0.5-goals-review-queue (commit d796852), frontend-only. Carry goal review-state into the model; Home needs-you queue+count include review-flagged goals (goal-aware Hero/QRow, deep-link to detail); Portfolio groups under Needs-your-attention; goal Detail sign-off bar (Approve/Request changes/Reopen) via the same setReview primitive; per-view nav badges (Artifacts=review artifacts, Goals=review goals). Verified hermetically: queue projection, deep-link, approve clears it from count/queue/portfolio/detail (bus review.state→approved), light+dark legible. codex review in flight; then PR→sirius gate.

PR #182 opened against v0.5 (branch v0.5-goals-review-queue @4c56d99). Codex 1 pass: 1 MAJOR (review goals appended after artifacts → never hero) fixed by carrying the goal revision + sorting the combined queue by recency, re-verified the goal leads as hero. Brief pr-182-brief (review-flagged). Pinged sirius to gate; CI running. On approval: merge → refresh :8765 → ping Lena #ui-feedback.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Shipped in PR #182 (squash 4926582) to v0.5. A goal flagged review.state=review now projects into the operator's needs-you/review queue (Home queue+count, Goals nav badge, Portfolio 'Needs your attention'), deep-links to the goal, and is signable in the Goals detail via a sign-off bar on the same setReview primitive (POST /api/artifacts/goal.<id>/review) — approving clears it from every surface. Per-view nav badges (Artifacts=review artifacts, Goals=review goals); roll()/home.goalRoll() kept in lockstep. Frontend-only; codex 1-pass MAJOR (goal-never-hero) fixed via revision-sorted combined queue. Verified hermetically + light/dark. Lena approved pr-182-brief; sirius gated.
<!-- SECTION:FINAL_SUMMARY:END -->

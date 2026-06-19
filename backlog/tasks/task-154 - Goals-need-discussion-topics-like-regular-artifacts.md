---
id: TASK-154
title: Goals need discussion topics like regular artifacts
status: In Progress
assignee: []
created_date: '2026-06-17 20:07'
updated_date: '2026-06-17 22:22'
labels:
  - feature
  - dash
  - goals
  - P2
  - needs-triage
  - 'slug:feat-goals-discussion-topics'
dependencies: []
priority: medium
ordinal: 144000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (outbox 2026-06-17): goals need discussion topics just like regular artifacts. A goal (goal.<id> artifact) should surface a discussion/comment topic in the dash goal-detail view, same as a regular artifact's companion topic (msg.topic.artifact.<name>) + inline discussion panel. Currently the Goals UI (#175) doesn't show a discussion thread for a goal, so there's nowhere to discuss/comment on a goal in-context (e.g. debate a criterion, leave a note).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A goal's detail view shows a discussion thread (messages/comments) like a regular artifact's inline discussion
- [ ] #2 Messages on the goal's companion topic render in the goal detail + can be posted from there
- [ ] #3 Consistent with the existing artifact discussion-topic convention (#142/#166)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered: Lena outbox 2026-06-17. Builds on Goals UI (#175) + artifact discussion-topic convention (#142/#166). Relates: [[goals-design]], [[goal.v0-5-0]].

Minimal slice (Lena's load-bearing catch via sirius 2026-06-17): the goal sign-off bar's request-changes had no feedback field, so the WHAT couldn't travel. Built on branch v0.5-goal-verdict-note (ac46d5c, off v0.5 post-#182). Added a feedback textarea to the goal SignOff (request-changes disabled until non-empty; approve optional); setReview(name,state,note) posts the note as a plain comment to the goal's companion topic msg.topic.artifact.goal.<id> BEFORE the verdict marker (backward-compat: no note ⇒ unchanged; artifacts unaffected). Verified hermetically: request-changes gated + note+marker both land on the topic in order; approve carries optional note; bar flips to changes; light+dark. REMAINING TASK-154: render the goal's discussion thread inline in the goal detail (so Lena sees the history, not just sends it). codex in flight; then PR→sirius gate.

PR #183 opened against v0.5 (branch v0.5-goal-verdict-note @2fb0898). Codex 1 pass: no crit/major; Q4 (note-publish-fail could skip the verdict marker) fixed; Q6b maxLength declined for consistency (artifact composer + /api/publish impose no limit). Brief pr-183-brief (review-flagged). Pinged sirius to gate. On approval: merge → refresh :8765 → ping Lena. REMAINING TASK-154: inline goal discussion-thread render in the detail.

Minimal slice SHIPPED in PR #183 (squash 22c75ee) to v0.5: goal sign-off feedback box — request-changes gated on a non-empty note, approve optional; the note posts to the goal's companion topic (msg.topic.artifact.goal.<id>) before the verdict marker via setReview(name,state,note). Lena approved pr-183-brief; sirius gated; live on :8765. Closes her one-way goal-feedback need. REMAINS (TASK-154 proper): render the goal's discussion thread INLINE in the goal detail (the companion-topic history shown like the artifact review rail), so the operator SEES the back-and-forth, not just sends it. Ticket stays open for that remainder.
<!-- SECTION:NOTES:END -->

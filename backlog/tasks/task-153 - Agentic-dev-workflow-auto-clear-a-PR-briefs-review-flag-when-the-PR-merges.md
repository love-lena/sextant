---
id: TASK-153
title: 'Agentic dev workflow: auto-clear a PR brief''s review flag when the PR merges'
status: To Do
assignee: []
created_date: '2026-06-17 19:25'
labels:
  - bug
  - workflow
  - review-queue
  - P2
  - ready-for-agent
  - 'slug:bug-workflow-pr-brief-stale-review-flag'
dependencies: []
priority: medium
ordinal: 143000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The agentic dev workflow creates a pr-NNN-brief artifact flagged review.state=review for its human-gate step, but the flag is never cleared after the PR merges. Stale 'needs review' flags accumulate: Lena's review queue showed 9 when only 3 were real (pr-176/177/178/179 briefs were stale on already-merged PRs; v0-5-train-read + -dash were crew SHIP reads, inputs not verdicts). violet down-ranks them in Home but that's only the projection — the raw dash count still includes them. Fix: when the workflow merges (or detects merged) a PR, clear its brief's review flag. Better: a pr-brief should only be review-flagged during the active human-gate window, not as a permanent state. Manually cleared the 6 stale flags 2026-06-17.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 When the agentic dev workflow's PR merges, the corresponding pr-NNN-brief review flag is cleared automatically
- [ ] #2 A merged-PR brief never lingers in the operator's needs-review queue
- [ ] #3 (consider) pr-briefs are review-flagged only during the transient human-gate window, not permanently
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered: Lena 2026-06-17 (9 review items, 3 real). Manually cleared pr-176/177/178/179 + v0-5-train-read(-dash). violet's 'un-orphan stalled flags' move ([[violet-architecture]]) is the downstream safety net; this is the upstream fix. Relates: agentic dev workflow, needs-review-convention.
<!-- SECTION:NOTES:END -->

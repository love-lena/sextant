---
id: TASK-142
title: 'Dash: ⌘K smart sort — recently-opened first, extensible ranking'
status: To Do
assignee: []
created_date: '2026-06-16 22:39'
labels:
  - feature
  - dash
  - ux
  - v0.5
  - 'slug:feat-dash-cmdk-smart-recency-sort'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 132000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena (#ui-feedback 2026-06-16): ⌘K should surface most-recently-opened views/items at the top, with a SMART/extensible sort — a ranking function that grows over time (recency now; later frecency, type-weight, pinned, current-context), NOT fixed recency-only. Track recent-opens (persisted MRU, localStorage); rank ⌘K results by a single pluggable scoring fn (recency-weighted first); empty-query ⌘K shows the MRU at top.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 ⌘K ranks recently-opened views/items first (empty + matching queries)
- [ ] #2 Ranking is one extensible scoring function (easy to add frecency/type-weight/pinned/context later), not hardcoded recency-only
- [ ] #3 Recent-opens persist across reloads
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
#ui-feedback (2026-06-16). orion captured it (folds into the ⌘K reskin). Lena wants the extensible-ranking FOUNDATION, not just recency. Claimed via backlog.counter CAS (142). Related: the ⌘K palette, v0-5-dash-design.
<!-- SECTION:NOTES:END -->

---
id: TASK-113
title: 'Dash: conversations should sort by most recent message on open'
status: Done
assignee: []
created_date: '2026-06-15 20:32'
updated_date: '2026-06-15 22:39'
labels:
  - bug
  - dash
  - ux
  - 'slug:bug-dash-convo-sort-recency'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 108000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The conversation list in the sidebar should sort by most recent message when the dash loads, so active topics bubble to the top. Currently the sort order is unclear on initial open — topics may appear in registration/discovery order rather than recency.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 On dash load, conversation list is ordered most-recent-first (by last message timestamp)
- [ ] #2 Sort order updates live as new messages arrive (existing convList sort by 'last' should cover this if 'last' is populated on load)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Filed from outbox 2026-06-15. The convList useMemo already sorts by last — root cause is likely that 'last' isn't populated from history on load, only from live SSE events.

2026-06-15: Orion has approach locked (seed last from latest ULID per subject on mount). Building on fresh main after #132 lands.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Fixed in PR #135: seeds convo sort key on mount from latest frame ULID per subject. Conversations sort most-recent-first on load. Also fixes 'Xm ago' display.
<!-- SECTION:FINAL_SUMMARY:END -->

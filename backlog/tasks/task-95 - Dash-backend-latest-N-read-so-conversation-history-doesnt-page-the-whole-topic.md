---
id: TASK-95
title: >-
  Dash: backend 'latest-N' read so conversation history doesn't page the whole
  topic
status: To Do
assignee: []
created_date: '2026-06-15 01:42'
updated_date: '2026-06-19 21:42'
labels:
  - feature
  - dash
  - performance
  - 'slug:feat-dash-latest-n-tail-read'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 97000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The dash /api/messages reads FORWARD from since (since=0 = oldest), with no tail mode. The frontend backfill now pages to the tail to show the newest messages (fix for the oldest-100 bug), but that loads a topic's ENTIRE history (bounded to 25 pages of 200) just to display the newest 200 — fine for hundreds of messages, wasteful for tens of thousands. Add a backend 'latest N' capability (e.g. a tail/last param on /api/messages, or a Bus method to read the last N of a subject) so the dash fetches the newest page directly in one request, and switch backfill to use it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 /api/messages can return the most recent N messages of a subject in one request (tail mode), without paging from since=0
- [ ] #2 frontend backfill uses the tail read (one request) instead of paging the full history
- [ ] #3 fake + real Bus covered; make lint && make test green
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up to the oldest-100 history bug fixed in this PR (frontend paging). The frontend paging is correct but O(history); this makes it O(1 page). Related: [[feat-plugin-dm-default-over-inbox]] era dash work.

Dash backend model revised by ADR-0041 / task-179 / task-180: the /api/* + SSE + bearer-token + internal/dashapi mechanism described here no longer applies. Re-frame the surviving need against the direct TS NATS-WebSocket client.
<!-- SECTION:NOTES:END -->

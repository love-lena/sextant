---
id: TASK-85
title: 'Web dash: bound the subject registry (eviction / stream-info)'
status: To Do
assignee: []
created_date: '2026-06-13 03:56'
updated_date: '2026-06-19 21:42'
labels:
  - feature
  - dash
  - frontend
  - perf
  - 'slug:feat-dash-subject-registry-bound'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 90000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
dash --serve keeps a per-subject counter for every msg.> frame for the life of the process (Server.Watch in internal/dashapi/subjects.go), subscribed with DeliverAll so it replays full history each start. At dash scale this is fine, but a long-lived dash on a busy bus accumulates one map entry per distinct subject with no eviction, and replays the entire backlog on every start. Bound it: evict stale/empty subjects, cap the map, or back /api/subjects with a JetStream stream-subjects query instead of a full replay.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the subject registry does not grow unbounded for a long-lived dash on a busy bus
- [ ] #2 startup does not require replaying the entire message backlog just to list subjects
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up from D2 [[feat-dash-web-ui-d2]] (TASK-71), flagged in final review. Acceptable at current scale.

Dash backend model revised by ADR-0041 / task-179 / task-180: the /api/* + SSE + bearer-token + internal/dashapi mechanism described here no longer applies. Re-frame the surviving need against the direct TS NATS-WebSocket client.
<!-- SECTION:NOTES:END -->

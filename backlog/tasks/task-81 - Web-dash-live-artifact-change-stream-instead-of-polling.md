---
id: TASK-81
title: 'Web dash: live artifact-change stream instead of polling'
status: To Do
assignee: []
created_date: '2026-06-13 03:34'
labels:
  - feature
  - dash
  - frontend
  - 'slug:feat-dash-artifact-live-stream'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 86000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
D2 picks up new/changed artifacts by polling /api/artifacts + /api/subjects every 4s (artifacts are KV, no message push). Replace the poll with a live artifact-change stream (server-side watch-all over the artifact KV, exposed as SSE like /api/stream) so the list updates instantly with no periodic refetches.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 new/updated/deleted artifacts appear ~instantly, no periodic polling
- [ ] #2 exposed by dash --serve (e.g. /api/artifacts/stream) backed by an SDK artifact watch-all
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up from D2 [[feat-dash-web-ui-d2]] (TASK-71). 4s poll is the shipped quick fix. SDK has WatchArtifact(name) — needs watch-all.
<!-- SECTION:NOTES:END -->

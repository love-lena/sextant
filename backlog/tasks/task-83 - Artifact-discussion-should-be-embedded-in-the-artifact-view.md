---
id: TASK-83
title: Artifact discussion should be embedded in the artifact view
status: To Do
assignee: []
created_date: '2026-06-13 03:50'
updated_date: '2026-06-19 21:42'
labels:
  - feature
  - dash
  - frontend
  - 'slug:feat-artifact-embedded-discussion'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 88000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
When viewing an artifact in the dash, the companion topic (msg.topic.artifact.<name>) discussion should appear inline — not as a separate conversation you navigate to. Read the artifact and see its review thread in the same view.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Opening an artifact in the dash shows its companion topic messages inline below (or alongside) the artifact body
- [ ] #2 The compose box in the artifact view publishes to the companion topic directly
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
The companion topic is already wired (msg.topic.artifact.<name>, TASK-66). The dash already has an SSE feed + artifact read. Embed the topic feed in the artifact panel.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Dash backend model revised by ADR-0041 / task-179 / task-180: the /api/* + SSE + bearer-token + internal/dashapi mechanism described here no longer applies. Re-frame the surviving need against the direct TS NATS-WebSocket client.
<!-- SECTION:NOTES:END -->

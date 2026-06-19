---
id: TASK-128
title: 'Dash: exclude artifact-discussion topics from the conversations list'
status: To Do
assignee: []
labels:
  - feature
  - dash
  - ux
  - 'slug:feat-dash-exclude-artifact-topics-from-convos'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Follow-up to TASK-83 (#142, inline artifact discussion). An artifact's companion
discussion topic (`msg.topic.artifact.<name>`) currently also appears in the
dash Conversations list, because the dash discovers + lists every `msg.>`
subject. Lena wants those companion topics EXCLUDED from the Conversations list
— they should only surface in the artifact view itself (the inline Discussion
panel from #142), not clutter the sidebar.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Subjects matching the artifact-companion pattern (`msg.topic.artifact.*`) are filtered OUT of the dash Conversations list
- [ ] #2 The artifact's discussion is still fully shown + postable in the artifact view's inline Discussion panel (TASK-83 unaffected)
- [ ] #3 Normal topics + DMs still appear in the Conversations list
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Frontend-only (the conversations-list builder in app.jsx / sidebar.jsx). Filter
the discovered-subjects → conversations mapping to drop `msg.topic.artifact.*`.
The companion topic is reached via `companionTopic(name)` in the artifact view.
Orion's dash lane (single-writer on app.jsx). Discovered: Lena 2026-06-16, after
#142 shipped in v0.4.1. Ref [[feat-dash-exclude-artifact-topics-from-convos]].
<!-- SECTION:NOTES:END -->

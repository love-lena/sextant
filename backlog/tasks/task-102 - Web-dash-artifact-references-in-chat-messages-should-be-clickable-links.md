---
id: TASK-102
title: 'Web dash: artifact references in chat messages should be clickable links'
status: To Do
assignee: []
created_date: '2026-06-15 03:02'
labels:
  - feature
  - dash
  - frontend
  - ux
  - 'slug:feat-dash-artifact-links-in-chat'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 97000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
When an agent mentions an artifact name in chat (e.g. 'see proposal-haiku-status-tracker'), the dash should render it as a clickable link that opens the artifact view. Currently artifact names in chat are plain text with no affordance to navigate to them.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Artifact names mentioned in chat messages are rendered as clickable links in the web dash
- [ ] #2 Clicking an artifact link navigates to (or opens) the artifact view
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Surfaced in outbox 2026-06-14. Related: [[feat-artifact-discussion-in-artifact-view]] (TASK-83)
<!-- SECTION:NOTES:END -->

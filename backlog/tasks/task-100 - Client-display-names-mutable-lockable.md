---
id: TASK-100
title: 'Client display names: mutable + lockable'
status: To Do
assignee: []
created_date: '2026-06-15 02:30'
labels:
  - feature
  - identity
  - protocol
  - 'slug:feat-client-display-name-mutable-lockable'
  - P2
  - ready-for-human
dependencies: []
priority: medium
ordinal: 95000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Clients need display names they can change at any time (not forced to re-mint), AND names should be lockable so a named agent's identity can't be spoofed by a rename. Two distinct capabilities: free rename + an operator lock that prevents further changes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A registered client can update its display name without re-minting
- [ ] #2 An operator can lock a client's display name so it cannot be changed by the client
- [ ] #3 Locked display names are visible as locked in the web dash and clients list
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Surfaced in outbox 2026-06-14. Related: [[bug-no-inplace-client-rename]] (TASK-61), [[feat-named-agent-stable-identity]] (TASK-76)
<!-- SECTION:NOTES:END -->

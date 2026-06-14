---
id: TASK-91
title: >-
  SDK: auto-subscribe the principal DM topic so an idle agent wakes on a
  principal DM
status: To Do
assignee: []
created_date: '2026-06-14 22:21'
labels:
  - feature
  - sdk
  - trust
  - bus
  - 'slug:feat-sdk-autosubscribe-principal-dm'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 93000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
TASK-90 made DMs the default for back-and-forth and the attest hook now stamps the principal DM, but only the INBOX (msg.client.<self>) auto-subscribes on connect (client.go subscribeDM) and bridges into the channel-wake path. A principal DM on the 2-party topic only wakes an agent that has explicitly message_subscribed to it (the startup skill now tells workers to do so). For true symmetry with the inbox, the SDK should also auto-subscribe the principal DM (sx.DMSubject(self, principal)) once the principal is known, and re-subscribe when principal.watch reports a change — so an idle agent wakes on a principal DM with zero setup.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 On connect, when a principal is designated and != self, the client auto-subscribes to sx.DMSubject(self, principal) and bridges it into the channel-wake path alongside the inbox
- [ ] #2 A principal re-designation (principal.watch) moves the auto-subscription to the new principal DM; the old one is dropped
- [ ] #3 Survives reconnect like the inbox auto-subscribe; covered by a test mirroring the inbox subscribeDM path
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Follow-up to [[feat-plugin-dm-default-over-inbox]] (TASK-90). Pairs with the attest principal-DM scan already landed. Without this, the startup skill's explicit subscribe is the workaround.
<!-- SECTION:NOTES:END -->

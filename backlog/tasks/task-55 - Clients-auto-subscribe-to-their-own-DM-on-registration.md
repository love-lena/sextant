---
id: TASK-55
title: Clients auto-subscribe to their own DM on registration
status: To Do
assignee: []
created_date: '2026-06-12 00:04'
labels:
  - feature
  - principal-trust
  - sdk
  - 'slug:feat-client-auto-subscribe-own-dm'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 61000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Every client should be reachable by direct message the moment it exists. On registration a client automatically subscribes to its own DM subject msg.client.<self> — zero-config inbound, so a principal (or any sender) can DM a client without the agent manually subscribing. Generic: no task-topic convention; the DM is the always-on inbound. Part of ADR-0030's generic-subscription decision.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 On successful registration, a client is subscribed to msg.client.<self> with no explicit subscribe call
- [ ] #2 A message published to a freshly-registered client's DM is delivered to it
- [ ] #3 The auto-subscription survives the normal connect/resume path
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
In the SDK registration/connect path, issue the self-DM subscription automatically once identity is established. Idempotent with an explicit subscribe.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Parent: task-53 ([[feat-principal-trust]]). ADR-0030 (generic subscription; auto-DM). Enables DM-targeting of auto-minted clients (ULID discoverable via clients_list). Blocked by: none.
<!-- SECTION:NOTES:END -->

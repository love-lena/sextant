---
id: TASK-6
title: 'Clients registry convention: self-register directory + ListClients read helper'
status: Done
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-12 17:47'
labels: []
milestone: 'M1: Core protocol + SDK'
dependencies: []
references:
  - docs/adr/0004-conventions-are-optional.md
  - docs/adr/0008-clients-are-processes.md
priority: medium
ordinal: 6000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A presence-only, self-maintained directory: each client self-registers a record (id, kind, epoch, SDK version) on connect and deregisters on Close — the write half already shipped in #68. The SDK adds a `ListClients(ctx) ([]ClientInfo, error)` read helper plus a public `ClientInfo` type. "Listed = registered and hasn't cleanly left"; a client that crashes without Close leaves a stale entry (accepted in M1). Subscriptions are deferred; heartbeat + read-time liveness + stale-entry reaping are deferred to TASK-20. Governed by ADR-0004 (conventions optional), ADR-0008 (clients are processes).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Record schema is {id, kind, epoch, sdk, connected_at}; subscriptions deferred
- [x] #2 Self-register on connect + deregister on Close (the directory convention; write half already in #68)
- [x] #3 SDK ships `ListClients(ctx) ([]ClientInfo, error)` + a public `ClientInfo` type exposing the record
- [x] #4 Heartbeat / read-time liveness / stale-entry reaping are out of M1 scope (TASK-20)
<!-- AC:END -->

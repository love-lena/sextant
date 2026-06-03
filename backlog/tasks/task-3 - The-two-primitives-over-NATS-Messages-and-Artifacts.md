---
id: TASK-3
title: 'The two primitives over NATS: Messages and Artifacts'
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
labels: []
milestone: 'M1: Core protocol + SDK'
dependencies: []
references:
  - docs/adr/0005-two-primitives.md
priority: high
ordinal: 3000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Messages = durable stream + client-controlled replay + subject conventions. Artifacts = KV, opaque, single-author, versioned, CAS, bounded history (~10), watchable. Governed by ADR-0005.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Messages: publish/subscribe with client-controlled backfill, zero per-agent server state
- [ ] #2 Artifacts: put is CAS, single-author, versioned, bounded history, watchable
<!-- AC:END -->

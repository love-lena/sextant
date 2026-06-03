---
id: TASK-3
title: 'The two primitives over NATS: Messages and Artifacts'
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-03 22:09'
labels: []
milestone: 'M1: Core protocol + SDK'
dependencies:
  - TASK-4
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
- [ ] #3 Decide artifact Value type: []byte vs Lexicon (JSON-vs-CBOR; 33% base64 cost of binary-in-JSON)
<!-- AC:END -->

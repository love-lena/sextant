---
id: TASK-174
title: 'TypeScript SDK: the primitive Wire client'
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
labels:
  - feature
  - sdk
  - typescript
  - 'slug:feat-ts-sdk-wire-client'
  - P1
  - ready-for-agent
dependencies:
  - TASK-172
  - TASK-173
ordinal: 164000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Build the TypeScript SDK (clients/ts/sdk) - the primitive Wire client: connect with its own scoped creds, publish/read/subscribe, the artifact operations, its own frame codec. A co-equal client, net-new (no current TS SDK; pre-cutover TS was replaced by the rewrite). Validate against a real bus over TCP. PRD doc-2, ADR-0041.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 clients/ts/sdk connects to a real bus and round-trips publish/read/subscribe + artifacts
- [ ] #2 it implements its own frame codec, verified against the frame lexicon + wire-level vectors
- [ ] #3 the TS suite passes the wire-level conformance vectors
<!-- AC:END -->

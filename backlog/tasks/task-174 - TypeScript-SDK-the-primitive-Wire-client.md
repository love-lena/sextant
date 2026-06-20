---
id: TASK-174
title: 'TypeScript SDK: the primitive Wire client'
status: In Progress
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 01:01'
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
- [ ] #4 the TS toolchain is stood up (runtime choice, package layout, test runner) under clients/ts
- [ ] #5 the TS SDK obtains its OWN scoped credentials (documented path), never the operator's ambient creds - verified as a distinct identity in the clients registry
- [ ] #6 cross-language round-trip proven on a real bus: a TS client publishes a message a Go client reads, and vice versa (not mocked)
- [ ] #7 a CI job builds and tests clients/ts and replays the conformance vectors on every push
<!-- AC:END -->

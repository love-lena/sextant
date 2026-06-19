---
id: TASK-175
title: Goals convention in TypeScript (co-equality proof)
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
labels:
  - feature
  - conventions
  - typescript
  - goals
  - conformance
  - 'slug:feat-ts-conv-goals'
  - P2
  - ready-for-agent
dependencies:
  - TASK-173
  - TASK-174
ordinal: 165000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Implement the goals convention in TypeScript (clients/ts/conventions/goals) over the TS SDK: hand-written verb logic against the shared lexicon, record types generated from it, passing the SAME goal conformance vectors as Go. The co-equality proof: two languages, one contract, identical wire behavior. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 clients/ts/conventions/goals implements the goal verbs over the TS SDK
- [ ] #2 the TS goals suite passes the identical conformance vectors the Go suite passes
- [ ] #3 record types are generated from the lexicon; verb logic is hand-written
<!-- AC:END -->

---
id: TASK-175
title: Goals convention in TypeScript (co-equality proof)
status: Done
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-20 02:00'
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
- [ ] #4 a single live scenario shows the TS and Go goals conventions writing/reading the same goal artifact on one bus with byte-identical record shapes - co-equality demonstrated, not two suites independently green
<!-- AC:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
TS goals convention - the co-equality PROOF - SHIPPED (PR #238 squash). clients/ts/conventions/goals over @sextant/sdk: hand-written verb logic mirroring conv/goals (SetCriterion + step-sentinels + proof-filter + rollup); record types GENERATED from goal.json via a TS lexgen (goal_gen.ts + no-drift test); a TS op-transcript recording-client + replay passing the IDENTICAL setCriterion.json vector the Go suite passes. LIVE co-equality (AC#4) PROVEN on a real bus: TS-wrote==Go-read AND Go-wrote==TS-read, BYTE-IDENTICAL canonical records both directions (via a gohelper Go binary driving the Go convention). Verified: TS suite 16/0; Go gate green after a fix round (gohelper main.go failed errcheck+staticcheck -> fixed 865c840, re-verified make lint clean by me). Both CI jobs green. Two languages, one lexicon contract, identical wire behavior.
<!-- SECTION:FINAL_SUMMARY:END -->

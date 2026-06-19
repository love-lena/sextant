---
id: TASK-173
title: 'Goals convention as a lexicon-defined library, with conformance vectors'
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
updated_date: '2026-06-19 21:31'
labels:
  - feature
  - conventions
  - goals
  - conformance
  - 'slug:feat-conv-goals-lexicon-library'
  - P1
  - ready-for-agent
dependencies:
  - TASK-172
  - TASK-183
ordinal: 163000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Establish the convention pattern end-to-end on goals: define the goal record + verb signatures in the lexicon; generate the Go record types; implement conv/goals collapsing the two hand-rolled halves (dash write half + violet read half) into one deep module - fixing the live label/state field-drift bug and the proof-filter scope divergence. Establish the conformance-vector format (recorded primitive-op transcripts) + runner; the Go suite replays them. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 goal record + verb signatures defined once in the lexicon; Go types generated from it
- [ ] #2 conv/goals is the single home for goal mechanics; the label/state bug and proof-filter divergence are gone
- [ ] #3 conformance vectors exist for the goal verbs and the Go suite passes them
- [ ] #4 the dash backend and violet consume conv/goals instead of hand-rolling goal logic
- [ ] #5 On the operator's live bus, the dash write path sets a goal criterion and violet's Home reads the same criterion's text+status with NO field-name fallback - a regression test sets a criterion via the dash path and asserts violet's reader sees identical text/status
- [ ] #6 the lexicon->Go type generation is built + documented (net-new: lexicons are read at runtime today) - a fresh agent knows where it lives and when it runs
- [ ] #7 after the swap, sextant dash --serve Goals and violet's curated Home render the same live goal identically
<!-- AC:END -->

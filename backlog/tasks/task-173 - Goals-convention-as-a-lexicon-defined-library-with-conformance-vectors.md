---
id: TASK-173
title: 'Goals convention as a lexicon-defined library, with conformance vectors'
status: To Do
assignee: []
created_date: '2026-06-19 21:11'
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
<!-- AC:END -->

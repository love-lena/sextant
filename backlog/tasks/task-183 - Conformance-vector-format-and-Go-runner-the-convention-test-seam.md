---
id: TASK-183
title: Conformance-vector format and Go runner (the convention test seam)
status: To Do
assignee: []
created_date: '2026-06-19 21:31'
labels:
  - feature
  - conformance
  - protocol
  - testing
  - 'slug:feat-conformance-vector-tooling'
  - P1
  - ready-for-agent
dependencies:
  - TASK-172
ordinal: 173000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Define the conformance-vector format and the Go runner every client implementation replays - the keystone test seam tasks 173/174/175 depend on. Decide the layout under protocol/conformance/, the op-transcript record shape (given record X, verb V produces these primitive operations), how a vector is recorded from a verb, and how the Go suite discovers + replays them. Extends the existing methods.json name-set conformance test (cmd/sextant/conformance_test.go) from operation-names to full transcripts. PRD doc-2.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 the vector format is specified (location under protocol/, the op-transcript record shape) and documented for other-language implementers
- [ ] #2 a Go runner discovers + replays vectors; a sample goal vector passes against the real goal convention
- [ ] #3 the existing methods.json name-set conformance test is subsumed or extended, not duplicated
<!-- AC:END -->

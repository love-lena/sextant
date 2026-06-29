---
id: TASK-247
title: run/v1 has no cross-language conformance vector
status: To Do
assignee: []
created_date: '2026-06-29 02:42'
labels:
  - test
  - workengine
  - conformance
  - P3
  - needs-triage
  - 'slug:feat-workengine-runv1-conformance-vector'
dependencies: []
priority: low
ordinal: 234000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The new sextant.workflow.run/v1 contract (run.start/run.event/run.control, coordinator + dash) has unit tests in Go and TS but NO op-transcript conformance vector replayed by both languages. The legacy workflow path's requestWorkflowStart.json was the only workflow conformance vector and was removed with the legacy retirement (TASK-234, PR #284). So Go/TS byte-identical drift on run/v1 is currently unpinned.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 protocol/conformance/vectors/ carries run/v1 vector(s) replayed by both the Go and TS workflow conformance suites
- [ ] #2 CI runs the run/v1 conformance in both languages
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: TASK-234 (PR #284) legacy retirement. Pre-existing gap (run/v1 never had vectors), surfaced when the old vector was removed. Relates to ADR-0041 co-equality + task-236.
<!-- SECTION:NOTES:END -->

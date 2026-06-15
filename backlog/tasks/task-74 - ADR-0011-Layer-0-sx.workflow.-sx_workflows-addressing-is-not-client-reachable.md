---
id: TASK-74
title: ADR-0011 Layer-0 sx.workflow.*/sx_workflows addressing is not client-reachable
status: To Do
assignee: []
created_date: '2026-06-13 03:01'
labels:
  - feature
  - protocol
  - docs
  - 'slug:feat-workflow-layer0-addressing'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 79000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
ADR-0011 names Layer-0 state as the sx_workflows bucket + sx.workflow.<id>.{control,events} subjects. But the sx.* namespace is bus-reserved (ADR-0012): clients publish only to msg.* and write only the ARTIFACTS bucket. So the documented Layer-0 reserved addressing is not reachable by a client today. The M5.4 coordinator correctly realized it over msg.* + a regular Artifact (ADR-0011 convention-over-primitives, no core change). Decision: either amend ADR-0011/0012 to make sx.workflow.* client-reachable (a core change), or update ADR-0011 to drop the reserved-namespace framing and bless the msg.* + Artifact realization as the canonical Layer-0.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 ADR-0011 and ADR-0012 are consistent: either the sx.workflow.* addressing is genuinely client-reachable (core change + ADR amendment), or ADR-0011 is updated to canonize the msg.* + Artifact realization
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Design decision for lena. Lean toward updating ADR-0011 to match the no-core-change realization — it already says convention-over-primitives and that's what shipped.
<!-- SECTION:PLAN:END -->

---
id: TASK-225
title: >-
  Work-engine: run checkpoints cannot be approved/resumed (the kind:checkpoint
  dead end)
status: In Progress
assignee: []
created_date: '2026-06-25 03:00'
updated_date: '2026-06-28 00:36'
labels:
  - feature
  - workflow
  - work-engine
  - dash
  - review
  - ready-for-human
  - 'slug:feat-run-checkpoint-resume'
  - P2
dependencies:
  - TASK-236
priority: medium
ordinal: 214000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
ADR-0048 runs carry kind:checkpoint steps (e.g. 'Operator approves the plan') that pause the run until the operator responds. The dash renders the waiting/'Needs you' state (workengine.jsx:217,85-88,785) and a run-topic composer, but the composer only publishes free-text chat.message (workengine.jsx:746-749) — there is no approve/advance action, no status write. A run parked at status:waiting can never proceed. Separately, the brief reader sets runResumes:true on an approve verdict (app.jsx:466) implying an approved checkpoint resumes a paused run, but nothing consumes it. The old engine has ctlApprove/ctlResume (apps/workflow/records.go:39-41, main.go:402-403) on the old contract the dash no longer drives. This folds in TASK-119 (approving a brief should fire the workflow gate) under the ADR-0048 contract.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The Run view exposes an 'approve checkpoint' / 'resume' action on a kind:checkpoint step
- [ ] #2 Approving writes the checkpoint step -> done and advances the run (or publishes a control message the coordinator honors)
- [ ] #3 Approving a brief tied to a run (review verdict with runResumes) resumes the paused run
- [ ] #4 A run that was waiting visibly continues in the dash after approval
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: capability-gap audit 2026-06-24. Depends on [[feat-run-executor-workflow-run-v1]] (TASK-236). Supersedes/folds in [[task-119]] under ADR-0048. Reuse old engine pause/approve semantics (apps/workflow). Relates ADR-0048.

Folded into the run executor (TASK-236, PR #279): a checkpoint-kind step parks the run at waiting; an operator run.control approve/resume advances it. Integration-tested (TestRun_CheckpointWaitsForApprove). Live-verify with TASK-236.
<!-- SECTION:NOTES:END -->

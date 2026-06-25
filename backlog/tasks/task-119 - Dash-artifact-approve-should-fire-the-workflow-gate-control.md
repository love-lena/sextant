---
id: TASK-119
title: 'Dash: approving a brief artifact should fire the workflow gate control'
status: To Do
assignee: []
created_date: '2026-06-15 17:00'
updated_date: '2026-06-25 03:01'
labels:
  - feature
  - dash
  - workflow
  - ux
  - 'slug:feat-dash-approve-fires-gate'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 119000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
When a workflow parks at its human gate, it surfaces a brief artifact for review
and waits for a `workflow.control` `approve` on `msg.workflow.<id>.control`.
Today, approving that brief in the dash only writes the artifact's `review.state`
— it does NOT publish the gate control, so the workflow stays parked. This bit
the operator directly: she approved the favicon brief in the dash and said "i
did approve it", but the workflow never resumed because the gate control was a
separate, un-wired signal a human had to publish by hand.

Fix shape: when a brief artifact tied to a parked workflow gate is approved in
the dash, also publish the `workflow.control` `approve` to the gate's control
subject. The dash needs to know the artifact ↔ workflow-gate linkage (e.g. the
brief records the control subject, or the run artifact references the brief).
Approving in the UI should close the loop end to end: review → approve →
workflow resumes → PR opens.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Approving a gate-linked brief artifact in the dash publishes the workflow.control approve to msg.workflow.<id>.control
- [ ] #2 The artifact ↔ workflow-gate link is explicit (brief records the control subject, or the run artifact references the brief)
- [ ] #3 A non-gate artifact approval behaves as today (review.state only, no control published)
- [ ] #4 Request-changes on a gate-linked brief publishes the corresponding changes control with the feedback
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: the workflow-task89-favicon gate, 2026-06-15 — operator approved
the brief in the dash but the gate didn't fire (the control is a separate
subject). Cross-cutting: dash (artifact approve handler) + workflow (the gate
contract). Ref: [[project_m5_spawn_spike_shipped.md]],
[[project_agentic_dev_workflow.md]].

2026-06-24 capability-gap audit: this is the pre-ADR-0048 framing of the same capability. Under the run-record contract it becomes 'approving a checkpoint step (incl. a brief) advances/resumes the run'. Folded into [[feat-run-checkpoint-resume]] (TASK-225), which depends on the run executor [[feat-run-executor-workflow-run-v1]] (TASK-224). Evidence the gate is still dead: brief reader sets runResumes:true (app.jsx:466) but nothing consumes it. Consider closing this in favour of TASK-225.
<!-- SECTION:NOTES:END -->

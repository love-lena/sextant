---
id: TASK-87
title: >-
  Haiku status-tracker agent: watches crew activity and updates goal/agent
  status on the bus
status: To Do
assignee: []
created_date: '2026-06-13 04:46'
updated_date: '2026-06-19 21:42'
labels:
  - P2
dependencies: []
priority: high
ordinal: 92000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A lightweight always-on agent (Haiku-class, fast + cheap) that observes crew topics, infers goal/task status transitions from activity, and publishes goal.update events so the dash Goals panel stays current without crew agents manually updating status. The tracker is the single writer for status transitions; crew agents just do work and the tracker observes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A Haiku-model agent runs as a long-lived bus client, subscribed to crew + topic channels
- [ ] #2 When it observes activity indicating a status change (agent starts working, asks lena a question, reports done), it publishes a goal.update event for the relevant goal
- [ ] #3 The dash Goals panel reflects status updates from the tracker in real time
- [ ] #4 The tracker is cheap enough to run continuously (Haiku model, low token usage)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Depends on TASK-84 (goal lexicon). Build as a supervisor-pattern agent using the spawn infrastructure. Subscribe to relevant topics, parse activity, emit goal.update records.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Proposal artifact 'proposal-haiku-status-tracker' drafted by orion (2026-06-14, requested via sirius): scopes the tracker as an observational signaling client (not a mgmt plane), surfaces the goal-source and self-report-vs-observe forks, recommends landing TASK-84 (goal lexicon) first. Pending lena's sign-off before build.

Goals model is now conv/goals (task-173) under ADR-0041; the TASK-84 dependency is dangling/replaced. Confirm parked before any build - do not write a divergent goal shape.
<!-- SECTION:NOTES:END -->

---
id: TASK-137
title: Automations — run a workflow/prompt on a schedule or a bus-level hook
status: To Do
assignee: []
created_date: '2026-06-16 21:29'
updated_date: '2026-06-25 03:01'
labels:
  - feature
  - automations
  - workflow
  - bus
  - design
  - 'slug:feat-bus-automations-scheduled-and-hooked'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 127000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Lena's idea (#outbox 2026-06-16): an automations capability — run a workflow or prompt on a TRIGGER, either (a) a schedule (cron-like) or (b) a bus-level hook (a bus event: a new artifact/message matching a shape on a subject). Example: every time a new task artifact is written to the bus, auto-trigger a triage workflow/prompt on it. Bus-native, event-driven + scheduled automation. Builds on the existing workflow primitive + bus subscriptions + scheduling. Composes with TASK-131 (backlog on the bus): 'new task -> triage' becomes a bus-hook automation. Design+security depth: the trigger model (subject/event patterns + schedules), WHO registers automations + the identity/authority a run acts as (signal-not-manage — it cooperates, never seizes control over agents), idempotency/dedup, the run record.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Register an automation that runs a workflow/prompt on a schedule (cron-like)
- [ ] #2 Register an automation triggered by a bus event (a new artifact/message matching a pattern on a subject)
- [ ] #3 The example works: a new task artifact on the bus triggers a triage workflow/prompt
- [ ] #4 The automation's trigger model + the identity/authority it runs as are explicit (security; holds signal-not-manage)
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: #outbox (2026-06-16). Claimed via backlog.counter CAS (137). Builds on the workflow primitive + bus subscriptions + CronCreate-style scheduling. Composes with TASK-131 (backlog on the bus). Design+security call (trigger model + automation authority) -> ready-for-human. Related: the agentic workflow harness, the triage skill.

2026-06-24 capability-gap audit: the dash now AUTHORS triggers on workflow templates (workengine.jsx:114,541 — e.g. 'On ticket labelled ready-for-agent', 'On nightly schedule') and persists them to workflow.template.<slug>, but nothing reads sextant.workflow.template/v1 to fire a run on any schedule/event (grep: zero Go consumers). Also a concrete dead control: TemplateDetail's 'Pause triggers' button is local React state only (workengine.jsx:711 setPaused), writes nothing, lost on reload. This ticket is the home for the trigger-execution engine; honoring authored triggers + persisting the paused flag belong here. Builds on the run executor [[feat-run-executor-workflow-run-v1]] (TASK-224). Bright-line: non-manual triggers are autonomous launch — needs explicit design.
<!-- SECTION:NOTES:END -->

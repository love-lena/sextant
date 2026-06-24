---
id: TASK-193
title: >-
  Run-record contract (ADR-0048): sextant.workflow.run/v1 + template, relates
  'toward', stop conditions
status: To Do
assignee: []
created_date: '2026-06-24 00:33'
labels:
  - ready-for-agent
dependencies: []
references:
  - docs/adr/0048-a-run-is-one-live-instance.md
priority: high
ordinal: 183000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Implement the approved ADR-0048 contract (PR #249) as a convention over Messages + Artifacts — no engine in core. A run is the latest-value artifact `sextant.workflow.run/v1` (ULID id, status, owner, template-or-null, steps); the reusable template is `sextant.workflow.template/v1` (name, triggers, steps — generic, no goal/criterion). A run links to the criteria it works toward via ADR-0035 `relates` with a new kind `toward`, bound at spawn. A run carries additive, disjunctive stop conditions as plain prompt strings (baseline done/blocked + optional plan-review); the outcome is carried in `status`. Active runs are discoverable as typed artifacts (run-index deferred). Generalizes today's `sextant.workflow/v1` — no records to migrate. Lives in/around the workflow coordinator + the conventions layer; conformance vectors per ADR-0041.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 sextant.workflow.run/v1 and sextant.workflow.template/v1 defined in the conventions layer with conformance coverage.
- [ ] #2 A run declares relates [{goal, crit, kind:'toward'}] set at spawn; a criterion projects its 'toward' runs from the artifact side (never written on the criterion).
- [ ] #3 stop is an array of prompt strings: baseline done + blocked on every run; a template may add non-terminal ones (e.g. plan-review); the run carries the merged set.
- [ ] #4 Active runs listable by $type + live status; a template's run history = runs naming it; ad-hoc run = template:null.
- [ ] #5 No migration path; sextant.workflow/v1 superseded. Conforms to ADR-0048 + CONTEXT (Run/Workflow/relates toward).
<!-- AC:END -->

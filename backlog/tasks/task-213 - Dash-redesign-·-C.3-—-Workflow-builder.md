---
id: TASK-213
title: Dash redesign · C.3 — Workflow builder
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
updated_date: '2026-06-24 18:17'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-work-engine
dependencies:
  - TASK-193
  - TASK-220
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 203000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Authoring a workflow spec: describe it, generate/edit steps, set triggers, watch WORKFLOW.md render live. Nothing runs until saved. A workflow is generic/reusable and carries NO goal or criterion (ADR-0048). Parent: EPIC C (task-200). Covers AC §8.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S8.1 step 1-Describe: free-text description + Generate steps that drafts a step pipeline from prose (split on connectives; detect ask/review language for operator checkpoints)
- [ ] #2 S8.2 step 2-Steps: editable, drag-reorderable pipeline; each step has an editable label + remove; operator-checkpoint steps carry an ask-operator tag
- [ ] #3 S8.3 add affordances: +step (work) and +operator checkpoint (ask); a terminal write-the-stopping-brief step is always present, marked always-required, not removable
- [ ] #4 S8.4 step 3-Triggers: Manual always present + fixed; operator can add custom free-text triggers, toggle on/off, double-click to remove
- [ ] #5 S8.5 step 4-WORKFLOW.md: a generated spec preview updating live (front-matter name/triggers, numbered steps with ask-operator + stopping-brief lines)
- [ ] #6 S8.6 footer: Save as draft (back, no run) and Save workflow (persists + opens template detail); editing preserves identity
- [ ] #7 Per ADR-0048: persisted as sextant.workflow.template/v1; stop conditions on the template declare only its additions to the done/blocked baseline (plain prompt strings)
<!-- AC:END -->

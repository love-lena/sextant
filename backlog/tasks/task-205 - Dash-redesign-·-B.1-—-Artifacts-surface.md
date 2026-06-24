---
id: TASK-205
title: Dash redesign · B.1 — Artifacts surface
status: To Do
assignee: []
created_date: '2026-06-24 01:08'
labels:
  - dash-redesign
  - ready-for-agent
  - lane-review
dependencies:
  - TASK-192
references:
  - >-
    https://claude.ai/design/p/a879e5e0-7130-4a48-bc63-c65cfc9502ad?file=Sextant%20-%20UX%20Acceptance%20Criteria.html
priority: medium
ordinal: 195000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Versioned documents authored by the operator or a workflow; authored-by is a ULID, never a persona. Parent: EPIC B (task-199). Covers AC §18.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 S18.1 header with Import a file + New doc; New doc opens a blank Composer, Import opens an OS file picker
- [ ] #2 S18.2 Your drafts: operator drafts (visible only to them until ready), each with kind, edited-ago, Draft/Ready chip; rows open the Composer; most-recent first
- [ ] #3 S18.3 Filed: artifacts a run brought back, each with name, version chip, run ULID, goal; rows offer spawn-work and open; empty state explains
- [ ] #4 S18.4 Import: text files read into a Composer draft (contents pre-filled); binary files become an import draft with a metadata banner; picker resets after use
<!-- AC:END -->

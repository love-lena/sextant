---
id: TASK-12
title: Author initial AT-Proto lexicon defs
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-04 18:04'
labels: []
milestone: 'M2: MVP'
dependencies: []
references:
  - docs/adr/0006-wire-atom.md
priority: high
ordinal: 12000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Author lexicon JSON files (unenforced, by convention): message, request/response, sextant.workflow/v1, spawn-request, presence. Governed by ADR-0006.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
M2 subset: only the record shapes manual-comms needs — a chat message kind + the artifact record shape. spawn.request and workflow.event/envelope defer to M4.
<!-- SECTION:NOTES:END -->

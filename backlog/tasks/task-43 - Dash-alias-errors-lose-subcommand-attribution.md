---
id: TASK-43
title: 'Dash alias: errors lose subcommand attribution'
status: To Do
assignee: []
created_date: '2026-06-10 21:36'
labels:
  - bug
  - dash
  - cli
  - ergonomics
  - 'slug:bug-dash-alias-error-attribution'
  - P3
  - ready-for-agent
dependencies: []
priority: low
ordinal: 49000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
With the dash: package prefix dropped (PR #99 error-prefix cleanup), the standalone binary prints sextant-dash: connect: ... but the sextant dash alias prints sextant: connect: ... — the operator can't tell the failure came from the dash subcommand. Fix shape: the alias path should attribute the subcommand once (sextant dash: connect: ...), consistent with however other cmd/sextant subcommands attribute (check the house style first; if no subcommand attributes today, decide whether dash should).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Errors from sextant dash name the subcommand exactly once; standalone sextant-dash output unchanged
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: PR #99 fold review (2026-06-10), finding 8.
<!-- SECTION:NOTES:END -->

---
id: TASK-9
title: Per-client write-precision (own-row-only scoping)
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
labels: []
milestone: Open design questions
dependencies: []
references:
  - docs/adr/0012-reserved-namespace-and-authn.md
priority: medium
ordinal: 9000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The one deferral from ADR-0012: scope a client to its OWN sx_clients/sx_workflows rows via distinct named creds, so a client can't overwrite another's registry record. Governed by ADR-0012.
<!-- SECTION:DESCRIPTION:END -->

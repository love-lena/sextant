---
id: TASK-10
title: Clients-registry record schema + heartbeat cadence + liveness threshold
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-06 06:51'
labels: []
milestone: Open design questions
dependencies: []
references:
  - docs/adr/0008-clients-are-processes.md
priority: high
ordinal: 10000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Define the registry record fields, heartbeat interval, and the read-time freshness threshold that decides presence. Governed by ADR-0008.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
PARTIAL: the registry record schema + connection-derived presence shipped (ADR-0020, wireapi.ClientEntry, serve.go presence join). Heartbeat cadence + liveness threshold are NOT built — that half is TASK-20.
<!-- SECTION:NOTES:END -->

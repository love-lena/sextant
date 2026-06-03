---
id: TASK-6
title: >-
  Clients registry convention: self-register record + heartbeat; read-time
  liveness
status: To Do
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-03 22:09'
labels: []
milestone: 'M1: Core protocol + SDK'
dependencies:
  - TASK-3
references:
  - docs/adr/0004-conventions-are-optional.md
  - docs/adr/0008-clients-are-processes.md
priority: medium
ordinal: 6000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Each client self-registers a record (identity, kind, epoch, SDK version, subscriptions) + heartbeat; presence is read-time liveness. Governed by ADR-0004, ADR-0008.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Settle registry-record schema, heartbeat cadence, and read-time liveness threshold (open design questions)
<!-- AC:END -->

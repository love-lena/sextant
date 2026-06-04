---
id: TASK-4
title: 'Go SDK: connect handshake + domain verbs + cooperative drain'
status: Done
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-04 04:00'
labels: []
milestone: 'M1: Core protocol + SDK'
dependencies: []
references:
  - docs/adr/0008-clients-are-processes.md
  - docs/adr/0010-lifecycle-and-versioning.md
  - docs/adr/0013-multi-backend.md
priority: high
ordinal: 4000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
connect() does authn + epoch hard-gate + clock-skew announce; domain verbs publish/subscribe/put/get/watch (no backend types leak); control.drain default handler; reconnect (connection-loss != exit). Governed by ADR-0008, ADR-0010, ADR-0013.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Domain-verb API only; no NATS types in the public surface
- [ ] #2 Default control.drain handler self-exits gracefully; dropped connection reconnects
<!-- AC:END -->

---
id: TASK-1
title: 'Stand up the bus: sextant up (embedded NATS) + bootstrap the sx namespace'
status: Done
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-03 21:50'
labels: []
milestone: 'M1: Core protocol + SDK'
dependencies: []
references:
  - docs/adr/0007-bus-is-nats-no-daemon.md
  - docs/adr/0012-reserved-namespace-and-authn.md
priority: high
ordinal: 1000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
sextant up embeds NATS (JetStream+KV) and bootstraps the reserved sx namespace under an operator credential; clients connect with a client-tier credential. Governed by ADR-0007, ADR-0012.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Embedded NATS launches via sextant up
- [ ] #2 sx_clients/sx_workflows/sx_system created at bootstrap (operator cred)
- [ ] #3 Client cred denies sx_ bucket lifecycle + sx_system + sx.control.*, allows convention subjects/buckets
<!-- AC:END -->

---
id: TASK-8
title: Identity & credentials mechanics
status: Done
assignee: []
created_date: '2026-06-03 01:12'
updated_date: '2026-06-06 06:51'
labels: []
milestone: Open design questions
dependencies: []
references:
  - docs/adr/0012-reserved-namespace-and-authn.md
priority: high
ordinal: 8000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
dev-open vs sextant token; client name uniqueness; minting the operator vs client credential tiers; how the static sx guardrail permission is issued. Governed by ADR-0012.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Shipped (ADR-0020): the bus is the sole minter (signing keys never leave it); operator + enrollment credential tiers; clients.register issuance; retire; per-client allow-list = the unforgeable author. Name reservation ledger (reserveName).
<!-- SECTION:NOTES:END -->

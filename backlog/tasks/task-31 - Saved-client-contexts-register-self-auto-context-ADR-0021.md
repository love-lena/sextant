---
id: TASK-31
title: Saved client contexts + register --self auto-context (ADR-0021)
status: Done
assignee: []
created_date: '2026-06-06 06:51'
labels: []
dependencies: []
ordinal: 30000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
kubectl/nats-style local contexts so commands run with no connection flags; register --self auto-creates + activates one.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 sextant context add|use|list|current|delete
- [ ] #2 connFlags.resolve precedence: --creds/$SEXTANT_CREDS -> --context/$SEXTANT_CONTEXT -> active context
- [ ] #3 register --self writes creds to the context store + activates a context (held-mode register unchanged)
- [ ] #4 e2e hermetic via per-run $SEXTANT_HOME; bob runs bare
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Shipped via PRs #88/#89 (merged to rebuild). internal/clictx is the store; ADR-0021 accepted.
<!-- SECTION:NOTES:END -->

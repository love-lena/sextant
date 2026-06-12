---
id: TASK-65
title: >-
  Principal re-point co-sign: require the current principal to co-sign a
  re-designation
status: To Do
assignee: []
created_date: '2026-06-12 18:40'
labels:
  - feature
  - principal
  - security
  - 'slug:feat-principal-repoint-cosign'
  - P3
  - deferred
  - ready-for-human
dependencies: []
priority: low
ordinal: 71000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Optional escalation over [[feat-principal-hardening]] (TASK-64). Today re-pointing the principal is gated by operator credential + --force (ADR-0031), but whoever holds the operator credential (a file in the store) can move operator-equivalence. This proposes that re-designating an ALREADY-established principal additionally require the CURRENT principal client to co-sign — so moving operator-equivalence needs the current operator-equivalent seat's participation, not just the operator-creds file. Operator-cred-only re-point remains an audited break-glass for recovery (lost principal key).

Deferred by the principal (lena) 2026-06-12: A+B (TASK-64) ship first; C is the bigger change and needs a design decision on the co-sign wire mechanism and the break-glass path.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Re-designating an established principal requires a co-signature from the current principal client; operator-cred-only re-point remains an audited break-glass.
- [ ] #2 Break-glass (lost principal key) recovery path is documented and tested.
- [ ] #3 Design decision recorded in an ADR before implementation.
<!-- AC:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Optional escalation over [[feat-principal-hardening]] (TASK-64). Builds on [[feat-principal-designation]], [[feat-principal-trust]] (ADR-0030/0031). Deferred 2026-06-12. Renumbered from a transient TASK-62.
<!-- SECTION:NOTES:END -->

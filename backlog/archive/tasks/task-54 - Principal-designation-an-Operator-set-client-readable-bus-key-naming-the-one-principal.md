---
id: TASK-54
title: >-
  Principal designation: an Operator-set, client-readable bus key naming the one
  principal
status: Done
assignee: []
created_date: '2026-06-12 00:04'
updated_date: '2026-06-12 02:40'
labels:
  - feature
  - principal-trust
  - bus
  - auth
  - 'slug:feat-principal-designation'
  - P2
  - ready-for-agent
dependencies: []
priority: medium
ordinal: 60000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The bus records its one PRINCIPAL (a human's client ULID) in a client-readable, Operator-writable sx key — the same read-open / write-operator shape as the protocol epoch in sx_meta (ADR-0012/0015). Bootstrap defaults it to the operator's enrolled seat; an Operator-credentialed command sets/re-points it (the two-way door); any client discovers the principal on connect and watches for change. This is the spine of ADR-0030 — the auth hook and everything else read this key. Enforcement: only the Operator credential may write it; clients read but never set it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The principal ULID is stored in a client-readable, Operator-writable sx key; a client-tier credential can read it but cannot write it
- [x] #2 sextant principal set <ulid> (Operator-credentialed) sets/re-points the principal; sextant principal get reads it
- [x] #3 Bus bootstrap defaults the principal to the operator's enrolled human seat
- [x] #4 A connected client can discover the current principal and observe a change to it without reconnecting
- [x] #5 A non-operator (client-tier) attempt to set the principal is denied by the bus
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Add a principal key in the operator-writable/client-readable namespace (reuse sx_meta's pattern; choose the key name). Wire the operator CLI (principal get/set). Default at bootstrap to the enrolled seat. Expose discovery + a watch in the SDK. No new auth tier — reuse the ADR-0012 two-tier permissions.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented + verified on branch task-53-principal-trust (PR #109): f584014. gofumpt/vet + go test -race + e2e all green. Adversarial review: no Critical; trust model proven sound. Rides TASK-53 for human sign-off.
<!-- SECTION:NOTES:END -->

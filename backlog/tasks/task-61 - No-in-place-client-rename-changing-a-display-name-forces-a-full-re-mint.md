---
id: TASK-61
title: 'No in-place client rename: changing a display name forces a full re-mint'
status: To Do
assignee: []
created_date: '2026-06-12 17:46'
labels:
  - feature
  - identity
  - cli
  - ergonomics
  - 'slug:feat-client-rename'
  - P3
  - ready-for-human
dependencies: []
priority: low
ordinal: 67000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A client display name is baked into its credential JWT tag at mint time (internal/wireapi DisplayNameTag, encoded by pkg/bus/auth.go mintIdentity) and is immutable. There is no clients rename verb and no update RPC, so renaming a client means minting a brand-new identity (new ULID), switching to it via context_use, and retiring the old one. That changes the client bus address, breaks any DM addressed to the old ULID, and leaves an offline corpse in the directory each time. Hit directly on 2026-06-12 when the operator asked a worker to rename your client to something memorable -- the only path was mint + context_use + retire. Since the bus is the sole minter, it could instead reissue a credential for the SAME ULID with a new display_name tag (preserving address + presence + history).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An operator can change a client display name without changing its ULID (the bus reissues the credential for the same id with a new display_name tag)
- [ ] #2 After rename the client keeps its DM subject (msg.client.<id>) and directory history; no offline duplicate is created
- [ ] #3 A sextant clients rename <id> <name> verb (or a documented facet of the cred-reissue flow) drives it
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Decide rename-as-its-own-verb vs fold into cred reissue (TASK-16). If standalone: add a bus operation that re-mints a credential for an existing ULID with a new display_name tag, distribute the new creds, expose clients rename. Keep the ULID stable so addressing + presence survive. Core identity work -> serial (ADR-0022).
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: 2026-06-12 worker rename request (the canopus session). Related: TASK-16 (credential reissue/revoke -- likely the same flow), [[bug-context-add-missing-kind]], [[feat-display-name-uniqueness]]. Touches the core identity model -> serial/core work per ADR-0022.
<!-- SECTION:NOTES:END -->

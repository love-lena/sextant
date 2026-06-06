---
id: TASK-30
title: 'Client identity: ULID primary id + display_name'
status: Done
assignee: []
created_date: '2026-06-05 04:33'
updated_date: '2026-06-06 06:51'
labels: []
milestone: 'M2: MVP'
dependencies: []
priority: medium
ordinal: 29000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
ADR-0018 follow-on (the identity half, sequenced separately from the envelope/frame work). Standardize: ULID = guaranteed-unique-by-Sextant primary id; display_name = human-readable, unique-by-convention. Clients get a ULID identity (the authenticated creds JWT name) + a display_name; the registry is keyed by ULID; the frame author is the ULID; msg.client.<ULID>. Readable addressing resolves display_name->id via the registry. Artifacts already use ULID ids; this is the client half. Touches token minting, the registry, addressing, ADR-0012.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Shipped (ADR-0019/0020): client identity is a bus-minted ULID (the creds JWT user) + a display_name tag; registry keyed by ULID; frame author = ULID; per-client allow-list scopes by ULID.
<!-- SECTION:NOTES:END -->

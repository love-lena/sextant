---
id: TASK-30
title: 'Client identity: ULID primary id + display_name'
status: To Do
assignee: []
created_date: '2026-06-05 04:33'
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

---
id: TASK-29
title: >-
  Implement ADR-0018: the bus implements the operations over the backend
  interface
status: Done
assignee: []
created_date: '2026-06-05 04:33'
updated_date: '2026-06-06 06:51'
labels: []
milestone: 'M2: MVP'
dependencies: []
priority: high
ordinal: 28000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The core build behind ADR-0018. Today the SDK drives NATS directly; this makes the bus a process that IMPLEMENTS the operations and owns all access. Scope: (1) a backend interface (the semantic contract as a Go interface — append-to-log, cas-put, get, watch, list-keys — designed to the contract, pressure-tested against Redis, not NATS-shaped); the NATS module behind it. (2) The bus serves operations over the Wire API as a call (request->result), stamps the frame (id/kind/epoch/author + artifact revision/createdAt/updatedAt), enforces identity (author from the authenticated request) + the reserved namespace; nothing direct (reads/writes/subscriptions all served). (3) pkg/wire Envelope->Frame (sender->author, kind message|artifact, ULID ids; artifacts become frames at rest). (4) SDK reframed as a client of the bus's operations (not a NATS library). Source of truth: protocol/ + ADR-0018. Big — likely splits into sub-tasks (backend interface, the call transport, frame stamping, SDK rewrite).
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Shipped (ADR-0018/0019): the bus serves the protocol's operations over the backend interface (internal/backend + natsbackend), stamps frames (id/author/kind/epoch), enforces author from the authenticated subject, and the SDK is reframed as a bus client (pkg/sextant/call.go, internal/wireapi).
<!-- SECTION:NOTES:END -->

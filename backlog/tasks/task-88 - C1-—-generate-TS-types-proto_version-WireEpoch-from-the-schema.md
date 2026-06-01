---
id: TASK-88
title: C1 — generate TS types + proto_version + WireEpoch from the schema
status: To Do
assignee: []
created_date: '2026-05-29 14:55'
labels:
  - feature
  - control-plane
  - codegen
  - contract
  - 'slug:feat-ctl-c1-wire-codegen-ts'
  - P2
dependencies: []
priority: medium
ordinal: 88000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Extend the existing `payloads.go → schemas/*.json` generator
(`cmd/sextantproto-gen`) to also emit the **TypeScript types**, a generated
`proto_version`, and the `WireEpoch` constant consumed by
`clients/typescript` and the sidecar. Kills the Go↔TS hand-sync of envelope
/ frame types and `PROTO_VERSION`.

**Why:** the sidecar's NATS protocol is the *same kind* of Go↔TS contract as
the CLI's — it's the second consumer of the generated types, and the
strongest argument for generating them. Hand-syncing it is the drift class
principle 1 forbids.

**Fix shape:**
- Generator emits `.ts` types + `proto_version.ts` + `WireEpoch`;
  `clients/typescript` imports generated types instead of hand-written ones.
- Add the **CI schema-compat gate**: diff regenerated schemas vs committed;
  **fail the build if a breaking change lands without a `WireEpoch` bump**
  (breaking = removed/renamed field, type change, optional→required, removed
  enum value; ambiguity → bump). Sibling to `changelog entry required`.

**Acceptance:**
- **E2E:** regenerate, build the TS client + sidecar image, run a **real
  prompt round-trip** (CLI → daemon → sidecar → frames) using the generated
  types — the wire still works.
- **Regression:** a Go↔TS round-trip test over a **checked-in message
  corpus** passes; existing Go RPC + sidecar protocol unchanged; editing a
  payload struct + `go generate` updates the TS types with no hand edits.
  CI: a PR with a breaking schema change but no epoch bump **fails**.
- **Expected breakage:** the hand-written TS type/`PROTO_VERSION` modules are
  deleted/replaced — any consumer importing those exact internal paths breaks
  (internal only; no external consumers today).

**Depends on:** none (generator infra). **Sequencing:** Wave 1 (∥
[[feat-ctl-c0-container-spec-builder]]); before [[feat-ctl-c2-verbspec-table]].
Runtime epoch *checks* live in [[feat-ctl-f0-front-door-authz]] (envelope) +
[[feat-ctl-p2-drift]] (container label). Part of
[[feat-control-plane-milestone]].
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-ctl-c1-wire-codegen-ts.md
Discovered in: control-plane RFC §5.8
Original created_at: 2026-05-29T14:55:00-07:00
<!-- SECTION:NOTES:END -->

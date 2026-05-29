---
title:          C1 — generate TS types + proto_version + WireEpoch from the schema
status:         open
priority:       P2
created_at:     2026-05-29T14:55:00-07:00
labels:         [feature, control-plane, codegen, contract]
discovered_in:  control-plane RFC §5.8
---

Extend the existing `payloads.go → schemas/*.json` generator
(`cmd/sextantproto-gen`) to also emit the **TypeScript types**, a generated
`proto_version`, and the `WireEpoch` constant consumed by
`clients/typescript` and the sidecar. Kills the Go↔TS hand-sync of envelope
/ frame types and `PROTO_VERSION`.

**Why:** the sidecar's NATS protocol (frames/inbox/lifecycle/heartbeat) is
the *same kind* of Go↔TS contract as the CLI's — the sidecar is the second
consumer of the generated types, and the strongest argument for generating
them. Hand-syncing it is exactly the drift class principle 1 forbids.

**Fix shape:**
- Generator emits `.ts` types from the schemas; emit `proto_version.ts` and
  `WireEpoch`; `clients/typescript` imports generated types instead of
  hand-written ones.
- Add the **CI schema-compat gate**: diff regenerated schemas vs committed;
  **fail the build if a breaking change lands without a `WireEpoch` bump**
  (breaking = removed/renamed field, type change, optional→required, removed
  enum value; ambiguity → bump). Sibling to the `changelog entry required`
  gate.

**Acceptance:**
- Editing a payload struct + `go generate` updates the TS types; no
  hand-maintained `PROTO_VERSION`.
- A Go↔TS round-trip test over a checked-in message corpus passes.
- A PR with a breaking schema change but no epoch bump fails CI.

**Depends on:** none (generator infra). **Sequencing:** Wave 1 (∥
[[feat-cp-c0-container-spec-builder]]); before [[feat-cp-c2-verbspec-table]].
The runtime epoch *checks* live in [[feat-cp-f0-front-door-authz]] (envelope)
and [[feat-cp-p2-drift]] (container label). Part of
[[feat-control-plane-milestone]].

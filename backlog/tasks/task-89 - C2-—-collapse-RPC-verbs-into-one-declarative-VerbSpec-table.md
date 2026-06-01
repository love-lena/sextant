---
id: TASK-89
title: C2 — collapse RPC verbs into one declarative VerbSpec table
status: To Do
assignee: []
created_date: '2026-05-29 14:55'
labels:
  - feature
  - control-plane
  - contract
  - 'slug:feat-ctl-c2-verbspec-table'
  - P2
dependencies: []
priority: medium
ordinal: 89000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Replace the four parallel enumerations of the RPC surface — `Verb*` consts
(`pkg/rpc/types.go`), `CapFor`, handler registration
(`cmd/sextantd/rpc.go`), and the generator's hand-maintained type list — with
**one declarative table**: `{name, capability, handlerFactory, req, resp,
phase}`. Dispatch *iterates* it, `CapFor` *reads* it, schema-gen *walks its
types*. `phase` preserves the two-stage registration (initial / query verbs
vs lifecycle / container verbs).

**Why:** four places enumerate verbs/types today; adding a verb and
forgetting its handler, capability, or schema is a live drift class (the
generator's type-list is the hidden 4th copy). One entry ⇒ nothing to
forget.

**Fix shape:** define `VerbSpec`; build registration + `CapFor` + the gen
type-list by iterating it; migrate the existing verbs into the table.

**Acceptance:**
- **E2E:** daemon boots and **every existing verb dispatches** against a real
  daemon (spawn / list / get-status / prompt / restart / kill / archive /
  query-* …) with unchanged behavior and caps.
- **Regression:** a table-completeness test asserts every `VerbSpec` has a
  registered handler **and** a capability (no verb without a handler, no
  handler without a verb); schema-gen output is **byte-identical** for
  existing types (pure refactor); the two-phase registration order is
  preserved.
- **Expected breakage:** none.

**Depends on:** [[feat-ctl-c1-wire-codegen-ts]] (generator). **Sequencing:**
Wave 2. Touches `rpc.go`, which P0 also touches — **land before**
[[feat-ctl-p0-reconcile-spine]]. Part of [[feat-control-plane-milestone]].
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-ctl-c2-verbspec-table.md
Discovered in: control-plane RFC §5.8
Original created_at: 2026-05-29T14:55:00-07:00
<!-- SECTION:NOTES:END -->

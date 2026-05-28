---
title: Decouple the proto + TS-client version lines (gated, not backlog)
status: deferred
priority: P3
created_at: 2026-05-28T15:40-07:00
labels: [feature, versioning, protocol, build]
discovered_in: 2026-05-28 versioning-policy rewrite — `conventions/versioning.md` documents four version surfaces as the target, but two of them (proto, ts-client) are coupled-or-stale; this ticket tracks the gap deliberately rather than as active work
---

## Status: deferred — both remaining pieces are gated on a trigger

`conventions/versioning.md` adopts the target model (four version
surfaces, each bumped by its own consumer's breakage). Of the three
non-binary surfaces:

- **Sidecar self-report** — DONE. Fixed in
  [[bug-sidecar-version-string-stale]] (it was a stale-string bug, not
  a version-line decision). Sourced from `package.json` now.
- **Proto line** — deferred; gated on wire-format negotiation (below).
- **TS client library** — deferred; gated on a publish decision (below).

Neither remaining piece is actionable today. This ticket exists so the
gap is documented, not so it reads as pending work. Don't pick it up
without the trigger having fired.

## Proto line — gated on wire-format negotiation

**Why deferred.** The proto version is currently *decorative*: it's
stamped on every envelope, but the only checks anywhere are
`ProtoVersion == ""` (is it set at all). Nothing compares the value;
nothing rejects on a mismatch. There are no independent peers running
mismatched wire versions — daemon and CLI build from the same commit,
the sidecar is a pinned image. So coupling proto to the binary semver
costs nothing real today, and splitting it now solves a problem that
doesn't bite.

**Trigger.** When wire-format negotiation becomes a thing — peers
advertising supported proto versions and rejecting/adapting on
mismatch — the proto line needs to bump on its own discipline (additive
wire change → minor; removed/changed shape → major), independent of
`VERSION`. That negotiation feature is the real work; the version split
falls out of it. The `pkg/sextantproto/doc.go` comment already flags
this.

**Until then.** Keep `ProtoVersion` tracking the binary number; bump it
on the release cut and note the wire delta in the changelog `Changed`
section (as v0.3.0 did).

## TS client library — gated on a publish decision

**Why deferred.** A version number is a contract with a consumer.
`@sextant/client` has no external consumers today: it's an internal
workspace dependency the sidecar imports, never published, and its
version has never moved off `0.1.0`. Bumping it on every release would
be busywork on a number nobody reads.

**Trigger / decision.** Decide whether `@sextant/client` gets published.
- If **no** (likely, for now): leave it at `0.1.0`, treat it as an
  internal workspace dep, and consider this part closed.
- If **yes**: that's a "publish the TS client" initiative (npm
  publishConfig, a release step, a README/usage surface) where
  independent versioning tied to the exported API falls out naturally.
  File that as its own ticket when the decision is made.

## Doc reconciliation (when both above resolve)

Once the proto line is independent and the TS-client question is
settled, drop the "Current reality: partially coupled" caveat block in
`conventions/versioning.md` and the matching note in `CLAUDE.md`.

## Related

- `[[bug-sidecar-version-string-stale]]` — the one piece that was
  actually actionable; resolved.
- `conventions/versioning.md` — the target model this closes the gap to.
- `pkg/sextantproto/doc.go` — `ProtoVersion`; comment anticipates the
  split + wire-format-negotiation follow-up.

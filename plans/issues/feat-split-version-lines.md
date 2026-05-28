---
title: Split the four version surfaces onto independently-bumped lines
status: open
priority: P3
created_at: 2026-05-28T15:40-07:00
labels: [feature, versioning, protocol, build]
discovered_in: 2026-05-28 versioning-policy rewrite — `conventions/versioning.md` documents four version surfaces (binary / proto / ts-client / sidecar) as the target, but three of them are currently coupled-or-stale rather than independently bumped by their own contract's breakage
---

## Summary

`conventions/versioning.md` adopts the target model: four version
surfaces, each bumped by *its own* consumer's breakage. The docs are
written to the strict target; this ticket tracks closing the code gap.

Current reality as of v0.3.0:

| Surface | Where | State |
|---------|-------|-------|
| Binary semver | `VERSION` → `pkg/version.Version` | source of truth; correct |
| Proto version | `sextantproto.ProtoVersion` + TS `PROTO_VERSION` | **coupled** — tracks the binary number by convention, bumped in lockstep on the release cut |
| TS client library | `clients/typescript/package.json` `version` | **stale** — stuck at `0.1.0`, doesn't move with releases |
| Sidecar self-report | hardcoded string in `images/sidecar/entrypoint/src/index.ts` (~line 1188, `"sextant-sidecar 0.2.0 …"`) | **stale + hand-edited** — drifts on its own |

## Why this matters

A wire-shape change that's invisible to operators should bump the proto
line without forcing a binary bump (and vice versa). A change to
`@sextant/client`'s exported surface should bump the library line for
importers regardless of what the daemon did. Coupling them means every
number lies about some consumer. Today we paper over it by bumping proto
with the binary and ignoring the other two — fine at this scale, wrong
as soon as the rates diverge.

## Shape

1. **Proto line independence.** Give `ProtoVersion` its own bump
   discipline (additive wire change → minor; removed/changed shape →
   major) decoupled from `VERSION`. Decide whether `ProtoVersion` and
   the binary semver can share a value transiently or should be visibly
   distinct from the start. Wire-format negotiation (peers advertising
   supported proto versions) is the eventual MAJOR-proto trigger — file
   a sub-ticket if/when a proto MAJOR is actually needed.
2. **TS client library versioning.** Move `clients/typescript`'s
   `package.json` version on its own cadence tied to the exported
   surface. Decide if it's published or stays `private: true`; if
   published, wire a release step.
3. **Sidecar version from the build.** Source the sidecar self-report
   from a build-injected value (mirror the Go `-ldflags` approach)
   instead of a hand-edited string, so it can't drift.
4. **Doc reconciliation.** Once the lines are independent, drop the
   "Current reality: partially coupled" caveat block in
   `conventions/versioning.md` and the matching note in `CLAUDE.md`.

## Acceptance

- `ProtoVersion` bumps are driven by wire changes, not by the binary
  cut, and the policy doc reflects that without a "currently coupled"
  caveat.
- `clients/typescript` version moves with its exported surface.
- The sidecar version string is build-injected, not hand-edited.
- `conventions/versioning.md` "Current reality" caveat is removed.

## Related

- `conventions/versioning.md` — the target model this closes the gap to.
- `CLAUDE.md` § "Versioning + PR policy" — the short form.
- `pkg/sextantproto/doc.go` — `ProtoVersion` lives here; the comment
  already anticipates a split + wire-format-negotiation follow-up.

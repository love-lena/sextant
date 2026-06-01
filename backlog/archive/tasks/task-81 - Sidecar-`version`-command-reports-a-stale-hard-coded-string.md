---
id: TASK-81
title: Sidecar `version` command reports a stale hard-coded string
status: Done
assignee: []
created_date: '2026-05-28 15:50'
labels:
  - bug
  - sidecar
  - versioning
  - diagnostics
  - 'slug:bug-sidecar-version-string-stale'
  - P3
  - 'closed:resolved'
dependencies: []
priority: low
ordinal: 81000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution (2026-05-28)

`images/sidecar/entrypoint/src/version.ts` (new) reads the version from
`package.json` at runtime via `createRequire`, exported as
`SIDECAR_VERSION`. Both call sites — the `version` / `--version` command
and the MCP client-identity handshake — now reference it, so neither can
drift from the manifest or from each other. `test/version.test.ts` pins
`SIDECAR_VERSION === package.json.version`. Verified: `node dist/index.js
version` now prints `sextant-sidecar 0.1.0` (was the stale `0.2.0`).

## Summary

The sidecar entrypoint hard-coded its self-reported version in two
places that had already drifted apart:

- `version` / `--version` command →
  `"sextant-sidecar 0.2.0 (SDK driver wire-up)"`
- MCP client-identity handshake (`new MCPClient({ name, version })`) →
  `"0.1.0"`

`package.json` says `0.1.0`. So the `version` command reported `0.2.0`
(a number it inherited from the binary semver at some past hand-sync and
never updated), while the package itself and the MCP handshake said
`0.1.0`. Pure stale-data bug: the diagnostic lied, and the two strings
disagreed with each other.

## Why it's a bug, not a "version line"

Per `conventions/versioning.md`, the sidecar self-report is diagnostics,
not a contract with any consumer. The fix isn't a new bump discipline —
it's "stop hand-writing a string that drifts." Sourcing it from
`package.json` makes the manifest the single truth.

## Fix shape (as shipped)

1. `src/version.ts` — `createRequire(import.meta.url)` loads
   `../package.json`; export `SIDECAR_VERSION = pkg.version`. Runtime
   require (not a static JSON import) because the manifest lives outside
   tsconfig's `rootDir: ./src`. `../package.json` resolves identically
   from `src/` and the built `dist/`.
2. `index.ts` — both call sites reference `SIDECAR_VERSION`; dropped the
   stale `(SDK driver wire-up)` parenthetical.
3. `test/version.test.ts` — locks the no-drift invariant.

## Related

- `[[feat-split-version-lines]]` — parent; this was its only
  immediately-actionable piece.
- `conventions/versioning.md` — the four-surfaces model; sidecar is the
  diagnostics-only surface.
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/bug-sidecar-version-string-stale.md
Discovered in: 2026-05-28 versioning-policy review — split out of [[feat-split-version-lines]] because it's a stale-data bug, not a version-line design question
Original created_at: 2026-05-28T15:50-07:00
Resolved at: 2026-05-28T15:52-07:00
<!-- SECTION:NOTES:END -->

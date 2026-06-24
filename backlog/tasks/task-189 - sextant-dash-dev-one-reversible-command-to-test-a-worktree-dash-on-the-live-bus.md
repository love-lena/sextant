---
id: TASK-189
title: >-
  Document the side-by-side dev-dash workflow (--port 0 --ui); optional sextant
  dash dev wrapper
status: Done
assignee: []
created_date: '2026-06-23 19:34'
updated_date: '2026-06-24 01:01'
labels:
  - feature
  - dash
  - dx
  - ergonomics
  - docs
  - 'slug:feat-dash-side-by-side-dev'
  - P3
  - ready-for-agent
dependencies:
  - TASK-188
priority: low
ordinal: 179000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Side-by-side dev already works with ZERO new code and is the settled approach (validated live 2026-06-23: managed prod dash on :8765 + a dev server on a free port both serving against the same live bus, the dev one serving a worktree web/app including the new favicon). Because the dash holds no standing bus connection ([[feat-dash-stateless-mint-on-demand]]) and each browser tab is its own co-equal client, two dash servers on different ports coexist cleanly -- no swap, no taking prod down, A/B comparable. So this ticket is mostly DOCUMENTATION: capture the supported dev loop -- 'sextant-dash --port 0 --ui <worktree>/clients/go/apps/internal/dashapi/web/app' (the sextant-dash BINARY; `sextant dash` no longer serves after the split, see feat-dash-standalone-binary) -- in CONTEXT/mdbook (--ui serves the SPA/jsx/css/favicon from disk with no Go rebuild; rebuild the binary only for Go-side changes). OPTIONAL ergonomic sugar: a thin 'sextant-dash dev' mode that auto-picks a free port, defaults --ui to the worktree web/app, and prints a clear 'DEV on port N' line. No swap/restore logic -- that earlier framing is superseded by side-by-side (the filename's 'reversible command' wording is stale; this body is authoritative).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The side-by-side dev loop (`sextant-dash --port 0 --ui <dir>`) is documented in CONTEXT.md AND a new mdbook page wired into docs/book/src/SUMMARY.md, as the supported way to test a dev dash against the live bus — including when to use --ui (UI-only changes) vs a rebuild (Go-side changes), and that it is the sextant-dash binary (not `sextant dash`, which no longer serves); docgen/`make book` clean
- [ ] #2 OPTIONAL: a `sextant-dash dev` mode that auto-picks a free port, defaults --ui to the worktree web/app, and prints a DEV-on-port-N banner; no swap/restore logic. If not implemented, the PR reviewer accepts the ticket on AC#1 alone (this sugar is explicitly optional, not required for AFK completion)
<!-- AC:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Doc-first: write the dev-loop section in CONTEXT/mdbook. Optional: a thin sextant dash dev subcommand wrapping --serve --port 0 --ui.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Discovered in: design session 2026-06-23; validated live the same day (prod + dev side-by-side on different ports). Supersedes the original 'reversible swap wrapper' framing. The one-liner works as soon as [[feat-dash-standalone-binary]] lands; the 'managed prod' half assumes [[feat-dash-managed-component]]. Related: [[feat-dash-stateless-mint-on-demand]].
<!-- SECTION:NOTES:END -->

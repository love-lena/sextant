---
id: doc-3
title: Handoff - orchestrating the co-equal-client refactor (tasks 171-184)
type: guide
created_date: '2026-06-19 21:48'
---

You are the orchestrator. Drive **tasks 171-184** to completion by leading a team of agents. The decision is [ADR-0041](../../docs/adr/0041-clients-are-co-equal-across-languages.md); the plan is the PRD (doc-2). Tickets are the source of truth — read each one before acting.

## The order (respect blockers)
Critical path: **171 → 172 → 173 → 174 → 175 → 177 → 180**. Also: **183** before 173; **176** (spike) feeds 177; **178** after 177; **181 · 182 · 184** run in parallel once their blockers are done. Never start a ticket until its `Blocked by` tickets are Done. Check `backlog task <id> --plain` for deps.

## The bar (non-negotiable)
Every ticket's acceptance criteria require the feature to **work on the operator's live setup** — not "compiles / merged / unit tests green." Do not mark a ticket Done until you have *run it and observed the UX*. This is the whole point of the refactor; "technically done" is failure.

## How to lead the team
- **One fresh agent per grabbable ticket.** Spawn it with the ticket's full text + this doc + AGENTS.md/CONTEXT.md. Keep it *sticky* — the same agent fixes its own review feedback; don't redirect it mid-build.
- **Isolate parallel agents in their own git worktrees.** Concurrent agents on one checkout collide. One PR per ticket.
- **Only fan out unblocked tickets.** When a blocker merges, release the tickets it unblocks. Coordinate the team by message; track status on the board (`-s "In Progress"` on pickup, `-s Done --final-summary` when its ACs are met).
- **Verify before Done.** Run the ticket's operability AC yourself (or via a driven session); attach the evidence.

## Escalate to the operator (do NOT decide these alone)
- **task-171 residual** (post-merge): add the CONTEXT.md terms (conformance suite, co-equal client, conventions layer). (ADR-0041 is already accepted.)
- **task-181** — the 5 lint-calibration decisions (containedctx exclusions, globals allowance, no-new-pkg rule, error-wrapping reword, test-file exclusions) are `ready-for-human`.
- **task-180** — its ADR revising ADR-0032/0034 + the browser-credential model needs operator sign-off on merge.
- **Any new or changed exported interface** — house-style rule: interfaces escalate to the operator, never ship on autonomous judgment. **Canon (ADRs, CONTEXT.md) merges only with operator sign-off.**

## Disciplines
- The **conformance vectors** (task-183) are the cross-language test seam — build them first; every SDK replays them; passing them defines a co-equal client.
- `importcheck` enforces import direction (a convention imports the SDK only, never the bus). Extend it; don't bypass it.
- Keep ticket filenames **ASCII** (the backlog CLI inserts em-dashes — rename). Commit as the operator with the project footer. `make lint && make test && go test -tags e2e ./tests/e2e/` before pushing.
- `pkg/…` / `internal/dashapi` paths cited in older canon/tickets are stale post-move (task-172) — treat as historical.

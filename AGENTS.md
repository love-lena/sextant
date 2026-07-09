---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# AGENTS.md

Start here, then read [CONTEXT.md](CONTEXT.md) for the shared language.

You are working on Sextant — a protocol + SDK for AI agents to collaborate over
a bus.

## How we work
- **Two planes.** Detailed, prescriptive work (specs, plans, breakdowns) goes in
  the **ephemeral workspace** — gitignored, no approval needed. The **committed
  canon** (this repo) is short, low-prescriptive, and changes **only through a
  human-signed-off merge**. committed ⇔ signed-off.
- **The loop:** signed-off short ticket → ephemeral work-plan → build on a
  worktree → PR + any canon updates → human review = sign-off.
- **Always work on a worktree.** The primary checkout (`/Users/lena/dev/sextant`)
  stays on `main` and clean. *Every* tracked change — code, an ADR or doc edit,
  even a one-line ticket triage — happens on a sibling worktree and lands via PR;
  never commit to `main` from the primary checkout. Only the gitignored ephemeral
  workspace (`.work/`) and the bus are fair game in place. Start the worktree
  *before* the first tracked edit, not after you notice the checkout is dirty.
- A genuine architecture decision gets an **ADR** (the why) and updates
  **CONTEXT.md** / **mdbook** (the language / the how). The API is the
  authority; code conforms to the docs. ADRs are rare and Lena-signed-off
  directly — not a routine per-ticket or agent-default artifact (2026-07-09
  bankruptcy: the prior 50 drifted out of sync with the code faster than they
  added value; see [`docs/adr/README.md`](docs/adr/README.md)).

## Bright-line disciplines — hold these
They keep Sextant from regrowing what it deliberately is not:
- Signal + cooperate, never track + manage.
- Call functions, never manage processes or identities.
- Concept, not codegen.
- Engine as a library in a client, never in the core.
- Thin universal core + opinionated, forkable reference implementations.
- Abstract only against a second implementation.
- Primitives, not policy (content is opaque; no baked-in defaults).

## Where things live
- Decisions → `docs/adr/` ([index](docs/adr/README.md)).
- Shared language → [CONTEXT.md](CONTEXT.md).
- Human reference + API → `docs/book/` (mdbook) — *forthcoming*.
- What's next → tickets in **Linear** (team `Sextant`, key `SX`). Backlog.md
  was retired 2026-07-09 with no migration — this is a clean start.

## Agent skills
Per-repo config for the [mattpocock/skills](https://github.com/mattpocock/skills)
engineering skills (`to-issues`, `to-prd`, `triage`, `diagnose`, `tdd`, …).

### Issue tracker
Tickets live in **Linear** (team `Sextant`, key `SX`), not GitHub Issues,
via the `linear` MCP plugin. Backlog.md was retired 2026-07-09 with no
migration. See [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md).

### Triage labels
Five canonical roles under their default names (`needs-triage`, `needs-info`,
`ready-for-agent`, `ready-for-human`, `wontfix`). See
[`docs/agents/triage-labels.md`](docs/agents/triage-labels.md).

### Domain docs
Single-context: one `CONTEXT.md` + `docs/adr/` at the root. See
[`docs/agents/domain.md`](docs/agents/domain.md).

---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# AGENTS.md

Start here, then read [CONTEXT.md](CONTEXT.md) for the shared language.

You are working on Sextant — a protocol + SDK for AI agents to collaborate over
a bus ([vision](docs/adr/0001-vision.md)).

## How we work
- **Two planes.** Detailed, prescriptive work (specs, plans, breakdowns) goes in
  the **ephemeral workspace** — gitignored, no approval needed. The **committed
  canon** (this repo) is short, low-prescriptive, and changes **only through a
  human-signed-off merge**. committed ⇔ signed-off.
- **The loop:** signed-off short Backlog task → ephemeral work-plan → build on a
  worktree → PR + any canon updates → human review = sign-off.
- A change to behaviour or the API gets an **ADR** (the why) and updates
  **CONTEXT.md** / **mdbook** (the language / the how). The API is the
  authority; code conforms to the docs.
- Full process: [ADR-0002](docs/adr/0002-documentation-and-process-layout.md).

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
- What's next → Backlog.md (`backlog` CLI) — *tooling setup forthcoming*.

## Agent skills
Per-repo config for the [mattpocock/skills](https://github.com/mattpocock/skills)
engineering skills (`to-issues`, `to-prd`, `triage`, `diagnose`, `tdd`, …).

### Issue tracker
Tickets live in **Backlog.md** (`backlog/`, driven by the `backlog` CLI), not
GitHub Issues. See [`docs/agents/issue-tracker.md`](docs/agents/issue-tracker.md).

### Triage labels
Five canonical roles under their default names (`needs-triage`, `needs-info`,
`ready-for-agent`, `ready-for-human`, `wontfix`). See
[`docs/agents/triage-labels.md`](docs/agents/triage-labels.md).

### Domain docs
Single-context: one `CONTEXT.md` + `docs/adr/` at the root. See
[`docs/agents/domain.md`](docs/agents/domain.md).

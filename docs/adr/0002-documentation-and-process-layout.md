---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Documentation and process layout

The working process is a foundation, not scaffolding: it keeps everything
committed **small enough for a human to read and sign off**, while letting agents
do detailed, prescriptive work among themselves. We split work into two planes
and a small committed canon.

**Two planes.** The *ephemeral* plane is the agent workspace — specs,
work-plans, breakdowns: format-free, freely prescriptive, and **never
committed** (a gitignored workspace now, the Sextant bus later). The *canon* is
everything in the repo: short and low-prescriptive, because omitting "how" is
what keeps it reviewable, and the implementing agent re-derives the "how"
ephemerally. **committed ⇔ signed-off** — the commit boundary is the ledger.

**The canon is four kinds, each answering one question; they reference, never
restate, each other.** *ADRs* — why we decided X (numbered, append-only;
supersede, don't edit). *CONTEXT.md* — the shared language (a glossary, nothing
else). *mdbook* (`docs/book/`) — how a human understands and uses it, and the
golden source of truth for the API (concise prose, complete API; the API is
documented first and code conforms to the docs). *Backlog.md* — what's next
(short tasks, each linking the ADR/CONTEXT that governs it).

**Entry point.** `AGENTS.md` is the agent entry point (`CLAUDE.md` symlinks to
it) and points at `CONTEXT.md`.

**The loop has two human gates.** A short Backlog task is signed off (gate 1) →
an agent writes an ephemeral work-plan → builds on a worktree → a human reviews the
PR plus any canon updates it carries and merges (gate 2 = sign-off). The canon
changes only through a signed-off merge; ephemeral artifacts never reach it.

**Sign-off mechanics.** Docs carry `status` / `signed_off_by` / `date`
frontmatter (`accepted` plus a name = signed off — this is our extension to the
Matt Pocock doc formats). Code is signed off by human PR review on a protected
`main`. The ephemeral plane is gitignored, so it cannot be committed by accident.

The trade-off is deliberate: this is heavier than committing freely, because a
core tenet is to keep committed work reviewable.

We **compose, rather than adopt wholesale**, the
[mattpocock/skills](https://github.com/mattpocock/skills) toolkit: this process
is the frame we own; `grill-with-docs` produces CONTEXT.md and ADRs, `to-prd` /
`to-issues` produce Backlog tasks, and `triage` manages them. Per-repo wiring
lives in `docs/agents/` (see `AGENTS.md` → "Agent skills").

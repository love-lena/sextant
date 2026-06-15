# Agentic dev workflow — design notes (TASK-95)

A reference **agentic dev workflow** on the sextant bus: an LLM **orchestrator** drives
a task from a one-line description to an open PR — plan → implement → codex-review → fix
→ brief → review → human-gate → release — by spawning a fresh worker per step and
resuming at each handoff. Driven by lena (the principal) over `msg.topic.orchestration-m5`
on 2026-06-14. Design artifact: `agentic-dev-workflow-design`.

## The decision that shapes everything: no bespoke engine

We do **not** build a workflow state machine in Go. The orchestration logic lives in an
**LLM orchestrator agent's reasoning** (a playbook prompt), which leverages the bus
primitives + sub-agent spawning. This is more in-spirit with sextant's bright lines
("primitives, not policy"; "engine as a library in a client, never in core"; "concept,
not codegen") and is far less code than the M5.4 Go coordinator (which stays on main as
the simple *linear* reference; it is not used here).

The orchestrator is the playbook `agentic-dev-workflow-orchestrator.md`.

## Architecture (lena's calls)

- **B — pure coordinator.** The orchestrator does not write code itself; it **spawns a
  fresh worker per step** (claude for plan/implement/fix/brief, codex for reviews) and
  coordinates them. (We considered A — the orchestrator does the claude work itself — but
  lena chose B.)
- **Sticky fixer.** A fresh agent per step, **except** the fixer/reviser/addresser is the
  **same** agent across the rounds of its revision loop (resumed). A persistent fixer
  remembers "I already tried X", which **dampens infinite loops** (lena's rationale).
- **Real repo + real bus.** The test target is the **sextant repo itself**, on the real
  bus — full dogfood.
- **Guardrails (v1):** all work isolated to a git **worktree + feature branch** (never
  the operator's main checkout); **release = `gh pr create`** (open a PR, never merge,
  push to main, force-push, or tag); the release step is **gated on the principal's
  `approve`**.

## The pipeline (the orchestrator's playbook)

```
plan (superpowers writing-plans → PLAN.md)
  → implement (against PLAN.md)
  → [codex review ⇄ fix]            (sticky fixer; bounded ≤3 rounds, fail-loud)
  → brief (artifact brief-<id>)
  → [codex review-brief ⇄ revise]   (sticky reviser; bounded)
  → human GATE (approve / changes+feedback)   ⇄ address-feedback (sticky)
  → release (gh pr create)
```

`PLAN.md` + the feature branch + named artifacts (review notes, the brief) carry state
between steps — durable + observable, not in-memory context. Reviewers end their output
with `VERDICT: approved` / `VERDICT: changes-requested`.

## How a worker is spawned (composes M5.1 + M5.2)

The orchestrator is a **top-level** bus client (registered by the operator at launch), so
it may mint workers (ADR-0033: a non-spawned client may register; the fork-bomb fence
only blocks already-spawned clients). For each step it registers a **named** identity
(`planner`, `implementer`, `reviewer`, `fixer`, `briefer`, …) and launches that worker as
the right harness with least-privilege tools scoped to the worktree — the M5.1 nickname
path (AC#4, proven). Workers appear on the dash under their role names.

The harness gives the orchestrator tested helper commands so the LLM doesn't hand-roll
the mechanics: `wf-spawn <role> <claude|codex> <prompt>`, `wf-spawn-resume <role>
<prompt>` (the sticky path), `wf-event`, `wf-progress`, `wf-dm`.

## The human gate = resume at the handoff (M5.1 wake loop)

At the gate the orchestrator posts "ready for review" to the principal's DM + the progress
artifact, then **yields** (ends its turn). The **M5.1 `spawn-poc` supervisor** watches
`msg.workflow.<id>.control`; when the principal sends `approve` or `changes <feedback>`,
it re-invokes the orchestrator with `--resume` (the control text arrives as the wake
input), and the orchestrator continues — to release on approve, or to a sticky
`addresser` then back to the gate on changes. This is exactly the resume-at-each-handoff
loop the spawn spike proved (claude -p --resume rejoins under the same keyed identity).

## Build slices

- **S1 — playbook + harness + a token-free stub plumbing demo.** The demo validates the
  wiring without an LLM or token spend: worktree+branch setup, named-identity
  registration, the `wf-*` helpers, the spawn-poc gate control round-trip (a seeded
  `approve`), and the `gh pr create` path — all on a throwaway bus + throwaway repo. This
  is what proves the harness before any token is spent.
- **S2 — live run (operator-driven).** lena runs the harness on a real sextant task: the
  real claude/codex workers, the real bus, end-to-end to the gate; she approves; it opens
  a PR. (The operator safety classifier blocks an *unattended* session from launching
  autonomous editing agents, so the live run is hers to drive — same constraint as the M5
  live demo.)
- **S3 — later:** richer briefs, the human-feedback loop polish, and (separately gated)
  optional auto-merge once the loop is trusted.

## Why this is safe to run on the real repo

- Workers/orchestrator only ever write inside a throwaway worktree on a feature branch;
  main is never touched.
- The most dangerous step is opening a PR — reversible, and gated on lena's explicit
  approve.
- No merge/push-to-main/force-push/tag anywhere in v1.
- Every loop is bounded (fail-loud round caps), so a confused reviewer can't spin forever.

## Open / deferred

- v1 names workers via the nickname path (register + creds), not the M5.2 dispatcher's
  mint-on-behalf — equivalent identities, less machinery. Routing through the dispatcher
  is a possible later refinement.
- Auto-merge on approve (vs open-PR) is deliberately out of v1.

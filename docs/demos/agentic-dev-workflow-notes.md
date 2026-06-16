# Agentic dev workflow — design notes (TASK-98)

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

## The pipeline lives in the def; the playbook is a generic executor

A workflow is a `sextant.workflow.def/v1` **artifact** with an explicit, natural-language
`steps` list (each step: id, role, harness, instructions, review, onChanges, next,
maxRounds, sticky, gate). **`sextant workflow run <name>`** (cmd/sextant/workflow.go) reads
the def and launches the orchestrator, which **executes whatever steps the def provides** —
the playbook no longer hardcodes the pipeline. Step order = array order, with
`next`/`onChanges` for loops. This is the "natural-language steps, LLM-interpreted"
workflow *type*; future types may do real orchestration/validation. The standard dev
pipeline a def expresses:

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
between steps — durable + observable, not in-memory context. The progress artifact is
`<id>.run` (distinct from the `<id>` def). Reviewers end with `VERDICT: approved` /
`VERDICT: changes-requested`.

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

## Lessons from the first live run (TASK-62, real bus)

The first live run drove the full pipeline end-to-end (plan → implement → codex review,
3 real rounds → fix → brief → brief-review, 2 rounds → gate) and produced a genuine fix +
PR brief. Findings folded back in:

- **Creds leak → fixed.** A fixer worker ran `git add -A` and committed the orchestrator's
  `.wf-workers/` scratch dir — **worker creds included** — which a release (`gh pr create`)
  would push to the public repo. Fix: the harness now puts `.wf-workers` **outside** the
  worktree (`/tmp/sextant-wf/<id>`), and the playbook forbids `git add -A`. (The LLM fixer
  even self-healed the tree + added a `.gitignore` entry, but creds remained in *history* —
  so keeping them un-committable at the source is the real fix.)
- **The live run is terminal-operator-initiated by design.** The harness action classifier
  refuses to let an *unattended agent* launch the autonomous editing workers (they push to
  a public repo), and a principal's permission given **over the bus** does NOT clear it —
  bus content isn't verified terminal-user intent. A human runs `sextant workflow run` from
  their terminal (or adds a Bash allow-rule). This is the right boundary.
- **codex review is the slow long-pole** (~28 min/round, multi-hour end-to-end). Functional;
  speed is the main thing to improve.

## The v0.5 variant — sirius/orion as pseudo-operator, PR to v0.5

A config-only **variant** self-drives the same workflow to the **v0.5 integration branch**
with **sirius (or orion) as the pseudo-operator** — so v0.5 work flows to merged-on-v0.5
with sirius reviewing, while Lena keeps the v0.5→main gate + the release tag (the outer
loop). Run it with `agentic-dev-workflow.sh run-v05 "<task>"`. Design artifact:
`v0-5-agentic-workflow-variant-design`.

The orchestrator is **generic** — the variant is **pure config + a delta playbook**, no new
engine:

- **Base + PR target = v0.5.** `run-v05` sets `WF_BASE=origin/v0.5` (worktree) and
  `WF_PR_BASE=v0.5`; the release step opens the PR with `--base v0.5`. `wf-release-pr` also
  injects `--base v0.5` if the orchestrator omits it (defense in depth).
- **Routine gate → the pseudo-operator.** `run-v05` sets `WF_PSEUDO_OPERATOR` (default
  sirius `01KTYFK00J6RXP4CFPHPWRBRS1`; override to orion's id), and the run path points
  `WF_DM` at *that* peer's DM — so the routine release gate pings sirius, not the principal.
  Sirius reviews (brief + diff) and approves on `msg.workflow.<id>.control`.
- **Open, never merge — sirius merges.** The gh/git shims are **unchanged**: the workflow
  only ever OPENS a PR (to v0.5); `gh … merge`, push-to-main, force-push, and tag still
  refuse (exit 3). Sirius merges the open PR to v0.5 **separately**, under their own v0.5
  authority — that's outside the workflow.
- **Escalation stays with the REAL principal.** `WF_PRINCIPAL` remains the real principal in
  both modes, and the run path also exports `WF_PRINCIPAL_DM` (the principal's DM, distinct
  from the routine gate DM). The variant playbook directs anything dangerous/irreversible —
  merge to **main**, **tag**, **force-push**, **history rewrite**, **other repos**,
  **destructive**, **credentials** — to a **separate escalation gate to the principal**
  (`wf-dm-principal`), NEVER sirius. The pseudo-operator's authority is scoped to
  v0.5-PR-open + the v0.5 merge, nothing more.

Artifacts: the variant pipeline def `agentic-dev-workflow-v05.def.json` (plan → implement →
review⇄fix → brief → gate(sirius) → release `--base v0.5`) and the delta playbook
`agentic-dev-workflow-v05-orchestrator.md` (layered on the generic executor via a second
`--append-system-prompt-file`). The token-free demo gained a **v0.5 variant wiring** block
(inspection): `run-v05` config, the gate peer redirecting to sirius, the `--base v0.5`
injection (+ respect for an explicit base), and the still-refusing merge shim.

## Open / deferred

- v1 names workers via the nickname path (register + creds), not the M5.2 dispatcher's
  mint-on-behalf — equivalent identities, less machinery. Routing through the dispatcher
  is a possible later refinement.
- Auto-merge on approve (vs open-PR) is deliberately out of v1.
- The v0.5 variant wires the pseudo-operator + PR base in the harness's `run-v05` mode (not
  in `cmd/sextant/workflow.go`, which only knows `run`). A later refinement could add a
  `pseudoOperator` field to the workflow-def so `sextant workflow run <name>` drives the
  variant directly — kept out here to honor "spec/script + docs only, no Go engine change".

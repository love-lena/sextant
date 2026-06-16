# Research-spike workflow — design notes

A reference **research-spike workflow** on the sextant bus: an LLM **orchestrator** drives
a research **question** to two comparable reports — research → rewrite — by spawning a
fresh worker per step. It is a close adaptation of the agentic-dev-workflow harness
(`agentic-dev-workflow.{sh,*-orchestrator.md,*-notes.md}`), simplified to a two-step,
artifact-only pipeline.

## The decision that shapes everything: no bespoke engine

Same as the agentic dev workflow — we do **not** build a workflow state machine in Go. The
orchestration logic lives in an **LLM orchestrator agent's reasoning** (the playbook
`research-spike-orchestrator.md`), leveraging the bus primitives + sub-agent spawning. In
spirit with sextant's bright lines ("primitives, not policy"; "engine as a library in a
client, never in core"; "concept, not codegen").

## The pipeline

```
research (claude: web research → artifact research-report)
  → rewrite (codex/gpt-5.5: read research-report + the question → artifact research-report-gpt5)
```

1. **research** — a **claude** worker researches the question with `WebSearch` + `WebFetch`
   + `Read` + the sextant artifact tools, and writes its findings as a `document` artifact
   named **`research-report`** (title + body).
2. **rewrite** — a **codex** worker on **gpt-5.5** (`codex exec --model gpt-5.5`) reads
   `research-report` + the original question and rewrites the report **from scratch** as
   its own independent version, writing a second `document` artifact named
   **`research-report-gpt5`**. Keeping both lets the operator compare Claude's vs gpt-5.5's
   report side by side.

The two named artifacts carry state between steps — durable + observable, not in-memory
context. The progress artifact is `<id>.run` (distinct from the `<id>` def).

## What's different from the agentic dev workflow (simplifications)

The research spike deliberately drops everything the agentic harness needs for *code*:

- **No human gate.** No `wf-spawn-resume` sticky loop, no `spawn-poc` wake supervisor on
  `msg.workflow.<id>.control`, no DM "ready for review". The output is artifacts the
  operator reads when convenient; nothing is gated.
- **No PR / release step** and **no `gh` merge-guard shim.** Nothing is committed, pushed,
  or merged.
- **No git worktree mutation.** The research workers never edit the repo, so there is no
  `git worktree add`, no `--add-dir`, no feature branch, no `WF_WORKTREE`. The `git add -A`
  creds-leak risk from the agentic run's first dogfood simply does not exist here.
- **No review ⇄ fix loop.** The pipeline is a straight two-step hand-off, so there is no
  `VERDICT:` parsing, `onChanges`, or `maxRounds` round-cap.

What's **kept** verbatim (the harness backbone): `gen_helpers` (`_wf-esc`, `wf-event`,
`wf-dm`, `wf-progress`, `wf-spawn`), the named-identity registration via `wf-spawn`, the
MCP-config-per-worker pattern, the orchestrator turn loop with `--resume` across turns, and
the stub/live split. The supervisor loop is trimmed: its only non-terminal state is
`running` (resume to continue); there is no `gate` branch.

## How a worker is spawned (composes M5.1 + M5.2)

The orchestrator is a **top-level** bus client (registered by the operator at launch), so
it may mint workers (ADR-0033: a non-spawned client may register). For each step it
registers a **named** identity (`researcher`, `rewriter`) and launches that worker as the
right harness with least-privilege tools:

- **researcher (claude)** — `WebSearch,WebFetch,Read` + `mcp__sextant__artifact_*`. No
  `Edit`/`Write`/`Bash`: it researches and writes an artifact, nothing else.
- **rewriter (codex, gpt-5.5)** — the sextant MCP only (`artifact_get` to read the prior
  report, `artifact_create` to write its own). No file editing.

Workers appear on the dash under their role names.

## Build slices

- **S1 — playbook + harness + a token-free stub plumbing demo.** The demo validates the
  wiring without an LLM or token spend: named-identity registration, the `wf-*` helpers,
  the two-step pipeline shape, and — the load-bearing assertion — that BOTH artifacts
  (`research-report` + `research-report-gpt5`) are produced, on a throwaway bus. This is
  what proves the harness before any token is spent.
- **S2 — live run (operator-driven).** The operator runs the harness on a real question:
  a real claude researcher + a real codex (gpt-5.5) rewriter on the real bus, producing
  the two reports. The operator drives this (the safety classifier blocks an *unattended*
  session from launching autonomous spawning agents — same constraint as the M5 / agentic
  live demos).

## How to run

```sh
# token-free plumbing demo (CI-safe; proves the 2-step pipeline + both artifacts):
docs/demos/research-spike-workflow.sh demo

# live (operator-driven; needs claude + codex on PATH and SEXTANT_STORE pointed at the bus):
SEXTANT_STORE=<live-store> docs/demos/research-spike-workflow.sh run "<your question>"
```

Env overrides:

- `WF_CLAUDE_MODEL` — research worker model (default `claude-haiku-4-5`).
- `WF_CODEX_MODEL` — rewrite worker model (default `gpt-5.5`).
- `WF_ORCH_MODEL` — orchestrator model (default `claude-sonnet-4-6`).
- `WF_STEPS` — JSON `steps` array for the pipeline file (the def's steps).
- `WF_ID` — workflow id (default `rs<epoch>`); `WF_MAX_TURNS` — supervisor turn cap.

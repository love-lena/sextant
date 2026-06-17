# violet runtime — design notes (TASK-138 / TASK-144, PR 2)

The **runtime** for violet, the operator's assistant (ADR-0039). PR 1 shipped the
*convention* — the ADR, the `violet-curation` skill (the defend judgement), and
the `assistant` designation artifact. This PR makes violet **runnable**: a
role prompt, a launch recipe, and a supervisor that wakes violet on the operator's
DM and on a periodic defend tick. Built + validated on `v0.5`; it goes live on the
operator's bus only at **release** (ADR-0039 "When it goes live").

## The runtime-weight choice: reuse the agent runtime (LIGHTER)

violet runs as a **client like any other** — a Claude-Code-style agent connected
via the sextant plugin/MCP, driven by a **role prompt** — the *same* shape the crew
run and the *same* shape the agentic-dev-workflow orchestrator runs (a markdown
playbook appended to `claude -p`, woken by a supervisor loop). We do **not** build a
bespoke Go SDK program. This was sirius's resolution of the ADR's OPEN point #1
(runtime weight) — violet is reactive + periodic; it does not need a continuous
reasoning loop or new Go. Reusing the agent runtime keeps violet a convention over
the existing primitives (the bright line ADR-0039 holds) and is far less code.

(The Go SDK shape exists if a dedicated runner is ever wanted — `examples/quickstart`
is the minimal client. We did not need it; the `claude -p` + MCP + supervisor path is
proven by agentic-dev-workflow and is lighter to operate.)

## The shape (three pieces)

- **The role prompt — `violet-runtime.md`.** Combines violet's two duties: ANSWER
  (read-only — follow the operator DM, read the workspace, reply) and DEFEND (load
  the `violet-curation` skill, run a curation pass, write the curated `home`
  projection). It states the read-only boundary and the two auditable curation moves
  (down-rank, un-orphan) verbatim from the skill/ADR. Placed in `docs/demos/`
  alongside the agentic-dev-workflow orchestrator playbook — the repo's convention
  for an agent role prompt that a runtime appends to `claude -p`. The *judgement*
  stays in the plugin skill (`clients/claude-code/skills/violet-curation`); the role
  prompt loads it, so the skill remains the single source of the curation logic.

- **The defend tick + answer wake — `violet-runtime.sh` (the supervisor).** ONE
  loop unifies both duties: each iteration `wait_for_trigger` blocks until EITHER a
  **new operator DM** arrives (polled via a DM message-count cursor, so history is
  never replayed) OR the **periodic tick** (`VL_TICK`, default 15m) elapses. A DM →
  an ANSWER turn (`VL_WAKE_FROM` = operator); a tick → a DEFEND turn
  (`VL_WAKE_TEXT=__TICK__`). Each turn is one `claude -p … --resume`, so violet
  carries context (her last curation pass, the conversation) across turns. This is
  the same supervisor shape agentic-dev-workflow uses for its gate
  (`wait_for_control` + `run_state` + a resilient resume loop). We did **not** use
  `spawn-poc` for the wake: it delivers from the start of history (`DeliverAll`), so
  a long-lived "wait for the *next* trigger" loop would replay the same DM forever
  and never reach the tick — the count-cursor poll is correct and self-contained.

- **The launch recipe — `violet-runtime.sh live`.** Registers violet
  (`clients register violet --kind agent`), reads the principal, computes the
  operator DM, **creates the `assistant` designation artifact** (`{client_id,
  name:"violet", accent}`) — this is the release-time go-live step per ADR-0039 —
  then runs the supervisor under violet's own creds. Identity: violet's own minted
  client id (no operator authority, no privileged tier). It uses the active context's
  creds to register and the live `SEXTANT_STORE`.

## How validated (hermetic; no live LLM, no live bus)

`violet-runtime.sh demo` is self-validating on a **throwaway** bus. It stubs `claude`
with a deterministic stand-in that performs each duty's core over the real sextant
CLI (so we validate the supervisor loop + the bus writes, not the model), then asserts:

1. **DEFEND (first turn + tick)** — violet wrote the curated `home` artifact with a
   curated `greeting.note` and a ranked `pinned` (real-calls) block — the record the
   dash reads (`internal/dashapi/web/app/home.jsx`, `ctx.home`).
2. **ANSWER** — an operator DM woke violet, and it replied **on the DM** (read-only).
3. **TICK** — the periodic defend tick fired from the supervisor loop with no DM.
4. **WAKE** — bus activity (a DM) woke violet for an answer turn.

All five assertions pass, stable across repeated runs. Run it (from a built tree):

```sh
make build                                  # build sextant + sextant-mcp
docs/demos/violet-runtime.sh demo
```

The live LLM behaviour (real curation judgement, real answers) is not exercised in
the hermetic demo by design — that is the role-prompt + skill, validated separately
when violet runs live at release.

## What the dash already understands

The dash fetches `GET /api/artifacts/home` → `ctx.home` and re-fetches it on bus
activity. `home.jsx` already reads `ctx.home.greeting` (`{heading, note}`) and
`ctx.home.blocks` (`pinned`, `links`). violet's defend duty writes exactly that
record. The fuller wiring — the dash's "Needs you" hero/queue reading violet's
curated ranked set instead of the raw `review.state` list — is **orion's dash
stream** (ADR-0039 D7); until it lands, the pinned block is the durable curated
record and the dash shows violet's curated `greeting.note`.

## Open questions for sirius / Lena

1. **Curation pool source.** The role prompt has violet gather the candidate pool by
   `artifact_list` + `artifact_get` over the bus (reading each artifact's
   `review.state`). At many artifacts this is N+1 reads per tick. Fine at assistant
   scale; if it grows, a single dash-style projection (or a `review`-index artifact)
   would be the optimization. Flagging, not building.
2. **Designation timing.** `live` creates the `assistant` artifact as part of going
   live. If Lena wants the designation created/owned separately (e.g. by the operator
   CLI, not by violet's launcher), that's a one-line move — say so.
3. **Tick cadence.** Default `VL_TICK=15m`. Bus-activity-driven re-curation (beyond
   the DM wake — e.g. waking on any `review`-flag change) is deferred: TASK-137
   (automations / bus-hook triggers) is the natural home for an event-driven tick.
   Until then it's the periodic tick + the DM wake.

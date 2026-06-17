# violet — SDK-client handoff spec

> Status: handoff. The bash prototype (`docs/demos/violet-runtime-warm.sh` +
> `violet-runtime.md` + the `violet-curation` skill, branch
> `feat-violet-warm-session`) proved the warm pseudo-agent design end-to-end and
> surfaced the real production issues. We are **NOT** shipping the bash runtime —
> violet becomes a proper long-lived **SDK client** (Python or Go), built as its
> own milestone. This is what a fresh SDK builder needs.
>
> Keep `violet-runtime.md` (the role prompt) and `violet-curation` (the tunable
> judgement) — they are the durable, reusable parts; the SDK replaces only the
> bash wrapper.

## Architecture — one client, three internal roles

ONE registered bus client / ONE identity (`violet`, ADR-0039 — *a client like any
other*) + the `assistant` designation artifact. Everything below is internal to
the wrapper, invisible on the bus. Behind that one identity, three model roles:

1. **GATE (haiku)** — triages live bus events. Per event: is this *significant*
   enough to wake the deep agent? Cheap; runs only on pre-filtered candidates.
2. **HOME-MANAGER (sonnet)** — woken by the gate **only on a significant event**
   (plus a slow safety-net interval as fallback). Does the deep work: reads
   current workspace state, re-curates the operator's `home` projection (the
   defend duty, per the `violet-curation` skill), and produces the compact
   context snapshot.
3. **CONVERSATIONAL (haiku)** — answers operator DMs from the warm context the
   home-manager keeps fresh. No per-DM pre-read — the answer is immediate from
   context.

Flow: **gate (every candidate event) → wakes home-manager (only on significant) →
refreshes the context the conversational role answers from.**

### Two load-bearing patterns

- **Output-capture.** The runtime publishes violet's reply to the operator DM by
  reading it off the conversational session's output; the **model never calls
  publish**. (Live bug in the spike: the model forgot to publish → the operator
  saw nothing. The wrapper owning the publish makes a lost reply *structurally
  impossible*.) The same pattern delivers the home-manager's context snapshot
  (captured from its output, not written to a file — see Bugs #2).
- **Context-warm answers.** The home-manager injects the current workspace
  snapshot into the conversational context **before** any DM arrives, so
  answering is instant-from-context with no lookup step between her question and
  the answer.

## Live-bus fixes the bash impl required (do these from day one)

1. **Scoped subscription — NOT the firehose.** Do **not** watch `msg.topic.>`
   (everything). Watch only: the operator DM; `msg.topic.goals`; artifact
   review/discussion (`msg.topic.artifact.>`); crew coordination
   (`msg.topic.crew`). Drop the per-client/status firehose (`msg.client.*`,
   `agent.status` heartbeats) — violet never needs it.
2. **Cheap pre-filter BEFORE the gate LLM.** A keyword test on the event text
   (significance signals: *review, ready, approv, verdict, sign-off, blocked,
   goal, criterion, merged, gate, "waiting on you", "needs you"*; plus
   operator-authored). Only matches reach the haiku gate. Obvious noise (WIP
   chatter, heartbeats) is dropped with **no LLM turn**. The haiku gate must run
   **rarely**, not per event.
3. **Answer-preempt.** Operator answers **must** stay fast regardless of gate
   backlog. Check the operator DM first/continuously and run the answer turn
   immediately, ahead of draining gate events; cap gate work so a burst of bus
   traffic cannot delay an answer. In an SDK client this is natural concurrency
   (a dedicated DM consumer / priority queue / separate task) — see *Why SDK*.
4. **Per-frame cursor.** Track a real per-subject cursor (process each frame
   exactly once, in order). Do **not** read "the newest frame" (races when
   several land between polls) and never replay history. Seed cursors at startup.
   Subtleties found the hard way: an empty subject reports next-cursor `0` but its
   first frame lands at cursor `1` (clamp the seed to ≥1, else you re-read frame 1
   forever); and `sextant read` prints its `(N messages; next cursor M)` summary
   on **stderr**, not stdout.
5. **Ignore own events.** Filter frames authored by violet's own id, or her
   published reply re-triggers her (self-loop). The **bus-stamped author id is the
   only trust signal** — never what a message claims about itself.

## Hard requirements

- **Operator answers in a few seconds, EVEN UNDER live-bus load.** This is the
  v0.5.0 "violet responds fast" criterion and the bar the bash impl failed
  (44s → 85s on the busy bus). Non-negotiable.
- **Answer format: ≤ 250 characters, plain text, NO formatting except
  `[[wikilinks]]`** (no bold, headers, or bullet lists). Terse and direct; cite
  artifacts by exact name in `[[ ]]` so the dash linkifies them.
- **Accuracy strictly from current context.** Answer from the injected warm
  context only; if the answer isn't there, say "I'll check" rather than guessing
  from memory / stale / training knowledge. (Live miss: violet claimed a feature
  was "still shipping" when it had merged.)
- **signal-not-manage.** violet answers and curates a **projection** (the `home`
  artifact she owns + her two auditable curation moves). She **never** acts on the
  operator's behalf: never merges, approves, writes a verdict, or changes another
  owner's artifact/state. *A helper that answers and curates is categorically not
  an agent that acts.*

## Bugs the spike hit — don't repeat them

1. **`claude -p --output-format json` returns a JSON ARRAY of stream events**, not
   a single object. The original session-id extraction did `jq '.session_id'` on
   the array → empty → every turn re-ran the wrong branch. Lesson: verify the
   actual output shape, never assume it.
2. **The home-manager session had Read + artifact-MCP tools but NO file-write
   tool** → it could not write the context snapshot to a file as first designed.
   Fix: the snapshot is **output-captured** (the session emits it as its turn
   output; the wrapper persists + injects it). Don't design a role to write a file
   it has no tool for.
3. **Hermetic stubs masked real behavior TWICE** — (a) a stub emitting a single
   JSON object hid the array-shape bug; (b) a stub that wrote the snapshot file
   directly hid that the real session couldn't. Stubs must emit the **real** output
   shape and exercise the **real** capture path, or they validate nothing.
4. **A single-loop gate triaging EVERY event as an LLM turn STARVES answers on a
   busy bus.** This was the live failure: serial haiku turns over the
   `msg.topic.>` firehose → unbounded backlog → operator answers queue behind it
   (44s → 85s) and a review-ready event was missed for 90s. This is precisely why
   scoped subscription + cheap pre-filter + answer-preempt + concurrency matter —
   and the core reason to move to an SDK client.

## Why an SDK client (not bash)

The bash wrapper made the warm pattern work and is single-threaded:
`inject → block for result`. Answer-preempt, capping gate work, and "answers never
wait behind a gate turn" are awkward-to-impossible cleanly in one bash loop — a
gate or deep turn mid-flight still blocks a waiting answer. A long-lived SDK
process (Python/Go) gives real concurrency: a dedicated operator-DM consumer with
priority, the gate on its own worker with a bounded queue + pre-filter, the deep
refresh on another, all sharing the in-memory warm context. That is the shape that
meets "fast under load."

## Security (operator-flagged, TASK-158)

violet — and **every** agent — must run with its **own scoped creds**, **never the
principal's ambient creds**. No impersonation of the principal. violet talks to the
bus through the sextant MCP/SDK under violet's own registered identity; the
operator's creds are never handed to her process. General trap: a bare CLI/MCP call
with no pinned creds/context resolves the operator's real active context — always
pin violet's own creds.

## Reuse from the spike

- `docs/demos/violet-runtime.md` — the role prompt (answer / defend / gate duties,
  answer-first, ≤250c plain+wikilinks, answer-from-context, signal-not-manage).
  Port the prose; the SDK supplies the plumbing.
- `clients/claude-code/skills/violet-curation/SKILL.md` — the tunable curation
  judgement + event-significance rule (the gate's WAKE/SKIP defaults).
- the `violet-architecture` bus artifact — Lena's design (the pseudo-agent: one
  client, fronting models behind a wrapper).
- branch `feat-violet-warm-session` — the working bash spike (commits `d88cfd3` →
  `5b8f007` → `50724fc`, plus the in-progress event-driven gate work) as the
  executable reference for the proven mechanics.

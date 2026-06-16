---
status: proposed
date: 2026-06-15
---

# Goals are shared objectives, tracked by observation

[ADR-0034](0034-the-web-cockpit-rests-on-conventions-not-new-protocol.md) noted
the dash's **goal metrics** were a deliberate stub: the design had a Goals panel
but the bus had no primitive behind it. The status workstream then split in two.
The first half shipped (PR #132): **`agent.status`** is a per-agent *current
status* — what one agent is doing right now — published as the latest-value
artifact `status.<agent-id>`. This ADR records the second half (TASK-84): a
**goal** — a shared objective the crew works *toward* — and the **`goal.update`**
signal that reports its movement.

A goal is not an agent's status. `agent.status` answers "what is *this agent*
doing right now"; a goal answers "where does *this objective* stand, across
everyone contributing to it." Shipping v0.4.0, landing the goal/status
workstream, getting the dash to green — these are goals; many agents move them,
and no single agent's status captures them.

## The shape (two records, mirroring `agent.status`)

Both are lexicons — record conventions over the existing artifact and message
operations. The bus does not learn about goals; like every record, the content
is opaque to it ([primitives, not policy](../../CONTEXT.md)). Nothing in the
core protocol changes, the same way ADR-0034's cockpit added none.

**`goal`** — the objective and its current state, published as the latest-value
artifact **`goal.<goal-id>`** (the direct mirror of `status.<agent-id>`):

- `title` (required) — what the objective is.
- `state` (required) — a coarse enum → a dash pill: `pending · active · blocked ·
  done · dropped`.
- `progress` — optional short free string (`"3/5 tickets"`, `"60%"`), rendered as-is.
- `headline` — optional present-tense line of the latest movement.
- `owner`, `ref`, `updated`, `by` — accountable agent, related ticket/PR,
  timestamp, and the convenience author label (the bus-stamped artifact author
  stays authoritative).

**`goal.update`** — a *signalled transition*, published as a message on
**`msg.topic.goals`** (a topic anyone can follow). It is an observation that a
goal moved — `{goal, state, progress, headline, ref, updated, by}` — not the
goal's value. The status tracker (TASK-87) emits these by watching crew activity;
any agent or human may emit one too. The current value lives in the `goal.<id>`
artifact; the stream of `goal.update`s is what just happened. This is the same
current-value-artifact + observable-event-stream pairing the workflow harness
already uses (its `<id>.run` progress artifact alongside `msg.workflow.<id>.events`).

## It signals; it does not manage

This is the load-bearing line, and the reason the tracker is safe to run
always-on. A `goal.update` is a **signal an agent cooperates with**, never a
control the tracker exerts ([signal + cooperate, never track + manage](../../CONTEXT.md)).
The TASK-87 tracker observes the bus and *announces* what it infers — "PR #143
merged, so the hardening goal looks `done`" — on `msg.topic.goals`. As a
convenience it may also compare-and-set the `goal.<id>` artifact so the dash has
a current value without a human in the loop. But:

- The goal's **owner and the operator stay authoritative.** The artifact is a
  single-writer-by-convention current value, not a verdict; anyone may correct
  it, exactly as anyone may correct their own `status.<self>`.
- The tracker has **no authority over agents.** It does not assign, gate, or
  direct work. It reports. Agents do their work and the tracker observes — the
  inverse of a management plane.
- A `goal.update` for a goal with no artifact yet is a **proposal to open one**,
  not an instruction.

If we ever wanted enforcement (a goal that blocks work until met), that would be
a new, separately-argued decision — it is explicitly *out* here.

## Consequences

The dash Goals panel de-stubs by reading `goal.<id>` artifacts (the same way the
Crew panel reads `status.<id>`), with `goal.update` available as the live
movement feed. The TASK-87 tracker is the first emitter, but goals are authored
and corrected by anyone — a human can declare a goal, an owner can set its state,
the tracker keeps the convenience view current. Because a goal is an artifact,
setting it bumps a revision, which is fine for a latest-value record. The core
stays thin ([ADR-0022](0022-parallel-modules.md)): two lexicons, zero new
operations.

Links: [TASK-84] (this work), [TASK-87] (the Haiku tracker that emits
`goal.update`), [ADR-0034] (the cockpit stub this fills), the approved
`proposal-goal-status-lexicon` artifact (the design discussion), and the shipped
`agent.status` lexicon (PR #132) this mirrors.

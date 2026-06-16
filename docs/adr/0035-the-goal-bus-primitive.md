---
status: proposed
date: 2026-06-16
---

# The goal bus primitive

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
everyone contributing to it." Shipping v0.5.0, landing the goal primitive,
getting the dash to green — these are goals; many agents move them, and no single
agent's status captures one.

A goal is **an objective plus its acceptance criteria**, and its **status is
derived** from how many of those criteria are met. Both records are lexicons —
record conventions over the existing artifact and message operations. The bus
does not learn about goals; like every record, the content is opaque to it
([primitives, not policy](../../CONTEXT.md)). Nothing in the core protocol
changes, the same way ADR-0034's cockpit added none.

## A goal is a north-star and its criteria; status is derived

**`goal`** is the latest-value artifact **`goal.<goal-id>`** (the direct mirror
of `status.<agent-id>`). Its record is:

- `northstar` (required) — the objective in one line.
- `stream` — an optional grouping label so related goals read together (e.g.
  `v0.5`). A free label.
- `criteria` (required) — the acceptance criteria, each `{id, text, status,
  owner?}`. A criterion's `status` is one of `met · in-progress · waiting-on-you
  · blocked · not-started`.
- `updated`, `by` — timestamp and the convenience author label (the bus-stamped
  artifact author stays authoritative).

There is **no stored goal-status field**. The goal's standing is the **rollup of
its criteria** — *N of M met* — computed by whoever reads the artifact. A goal
with no criteria reads as not-started. Deriving rather than storing means the
goal can never disagree with its own criteria, and a UI is free to summarise the
rollup however it likes (a fraction, a bar, a pill) without the record taking a
position.

## Evidence is declared artifact-side, with a generic `relates`

A criterion does not store its evidence. Instead, **any artifact may declare what
it relates to**, and a criterion's evidence is *projected* from those
declarations. An artifact's record carries an optional

```
relates: [ { goal: <goal-id>, crit?: <crit-id>, kind: "proof" | "related" } ]
```

— a convention on records, content-opaque to the core (not a core schema
change). `kind:"proof"` means the artifact is evidence backing a *met* criterion
(a test run, a merged PR, a review); `kind:"related"` (the default) is a generic
"this artifact relates to goal X / criterion Y" association. A criterion's
proof set and related set are the artifacts whose `relates` point at it — read
from the artifact side, never written onto the criterion. Declaring the link on
the artifact keeps the goal record stable as evidence accrues, and lets one
artifact relate to several goals or criteria at once.

The one rule this earns is an **invariant: every *met* criterion has at least one
proof-kind artifact** — even if that artifact only records "ran these tests, they
passed." *Met* without evidence is not met. The invariant is a convention the
emitters honour, not something the bus enforces.

## Who marks a criterion met is fuzzy, by design

Flipping a criterion to *met* is deliberately **uncoded** — there is no
operator-gate baked into the primitive ([signal + cooperate, never track +
manage](../../CONTEXT.md)). Three ways coexist:

- **Self-serve.** A mechanically-testable criterion is satisfied by an agent that
  runs the check, writes a proof artifact, and flips the criterion to met.
- **Inferred.** An agent may read "a human approved this artifact" as grounds to
  mark the criterion it proves as met.
- **Operator.** The operator sets a criterion directly.

The goal artifact is single-writer *by convention*, the same as
`status.<self>`; anyone may correct it, and the owner and operator stay
authoritative throughout. Baking an approval gate into the primitive would make
it a management plane; leaving the judgement fuzzy keeps it a signal.

## The in-flight "how" is a workflow, not a new primitive

There is **no "workstream" primitive**. The work in flight toward a goal is the
existing **workflow** (ADR-0011): a workflow declares which criterion it advances
(via the same `relates` convention or its own field), and the criterion's
movement shows up as the workflow progresses. Goals describe the *outcome*; a
workflow is one *how*. Reusing the workflow primitive keeps the core thin
([ADR-0022](0022-modules-over-a-locked-core.md)) and avoids a third tracking shape.

## `goal.update` signals; it does not manage

**`goal.update`** is a *signalled transition*, published as a message on
**`msg.topic.goals`** (a topic anyone can follow). It is an observation that a
goal moved — `{goal, crit?, status?, headline, ref?, updated, by}` — not the
goal's value. Because the goal's standing is derived from its criteria, an update
headlines a *movement* (a criterion met, a goal opened) and optionally reports a
criterion's new `status`, rather than stamping a goal-level enum. The current
value lives in the `goal.<id>` artifact; the stream of `goal.update`s is what
just happened. This is the same current-value-artifact + observable-event-stream
pairing the workflow harness already uses (its run progress artifact alongside
`msg.workflow.<id>.events`).

This is the load-bearing line: a `goal.update` is a **signal an agent cooperates
with**, never a control anything exerts. The goal's **owner and the operator stay
authoritative**; nothing has authority over agents — a `goal.update` reports, it
never assigns, gates, or directs work. A `goal.update` for a goal with no
artifact yet is a **proposal to open one**, not an instruction. If we ever wanted
enforcement (a goal that blocks work until met), that would be a new,
separately-argued decision — it is explicitly *out* here.

## Goals are generic; they never require tickets

A goal references **artifacts** — its criteria's proofs and related work — and
**must not depend on backlog tickets**. Backlog is a dev tool for this repo;
Sextant ships and is applied elsewhere without it. An artifact's `relates` or a
`goal.update`'s `ref` *may* point at a ticket, but the primitive never requires
one. The goal primitive stands on artifacts and messages alone.

## Consequences

The dash Goals panel de-stubs by reading `goal.<id>` artifacts (the same way the
Crew panel reads `status.<id>`), deriving the rollup from the criteria, with
`goal.update` available as the live movement feed; the proof/related sets come
from scanning artifacts' `relates`. The consumers — the Goals UI and any future
tracker — are out of scope here; we abstract against them once they exist
([abstract only against a second implementation](../../CONTEXT.md)). Because a
goal is an artifact, setting it bumps a revision, which is fine for a latest-value
record. The core stays thin: two lexicons and a record convention, zero new
operations.

This **supersedes** the parked coarse-state goal model (a stored
`pending · active · blocked · done · dropped` enum plus a free progress string).
The carry-overs hold — goal-as-artifact `goal.<id>`, the `goal.update` stream on
`msg.topic.goals`, signal-not-manage, the thin core — and only the record content
changes: a stored state becomes a north-star plus criteria with a derived rollup.

Links: [TASK-84] (this work), [ADR-0034] (the cockpit stub this fills),
[ADR-0011] (the workflow that carries a goal's in-flight "how"), and the shipped
`agent.status` lexicon (PR #132) this mirrors.

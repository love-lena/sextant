---
status: accepted
signed_off_by: lena
date: 2026-06-23
---

# A run is one live instance of work

[ADR-0011](0011-workflows.md) made a workflow a convention over Messages and
Artifacts, run by a coordinator client, with a `sextant.workflow/v1` state
envelope. The work-engine surfaces of the operator dash need that envelope to
carry a little more, and need the language to separate the reusable *workflow*
from the live *run* of one. This records that shape. It stays a convention over
the two primitives — no engine in the substrate, nothing new in the locked core.

## A run is the instance; a workflow is the reusable template

A **run** is one live instance of work, identified by a ULID and described by
what it does: steps walking forward, an activity stream, the goal criteria it
works toward, and the conditions under which it may stop. It is the thing the
operator watches. A run may come from a reusable **workflow** — a `WORKFLOW.md`
template of trigger and steps, usable for any purpose and carrying no goal or
criterion of its own — or be **ad-hoc**, a single mobilized agent with no
template. Both are runs; both are observed the same way.

Today's `sextant.workflow/v1` envelope is already a run in all but name. It
becomes `sextant.workflow.run/v1`; the reusable template is
`sextant.workflow.template/v1`, both under the `sextant.workflow.*` namespace.
There are no existing records to preserve, so the run kind simply supersedes the
old one — nothing to migrate. A run is itself an artifact, so it is discoverable
the way every artifact is: the active runs are those of `$type`
`sextant.workflow.run/v1` whose status is live, read from the artifact list a
client already holds. An ad-hoc run carries `template: null`; a workflow's run
history is the runs whose `template` is its name. A dedicated run-index is
deferred until listing gets heavy.

## A run links to the criteria it works toward

The goal/criterion binding is not a bespoke field, and is never on the template —
a template is generic. It is made when a run is **spawned** and pointed at a
goal, and it reuses the artifact-side `relates` convention
([ADR-0035](0035-the-goal-bus-primitive.md)): a run declares
`relates: [{goal, crit, kind: "toward"}]` for each criterion it was pointed at —
a third `kind` beside `proof` and `related`. The review loop reads these links:
clearing a brief tied to a run advances each criterion it works toward and moves
the goal's rollup. A template's "feeds criteria" view is *derived* from where its
runs have been pointed, never declared on it.

## A run declares its stop conditions

A run carries its **stop conditions** — the set of states in which it may stop,
each a plain prompt the running agent reads, each requiring a brief posted to the
bus for operator review. They are **additive and disjunctive**: the runner may
stop the moment it meets any one, but must meet at least one — a run never halts
without posting the brief that justifies it. Every run carries the same baseline:
stop when the work is *done* (a brief with proof of success) or *blocked* (a
brief documenting why it cannot proceed). A workflow may add more — most commonly
a mid-execution *plan-review* pause that posts a design or plan brief for
operator feedback, then continues once the operator responds. There is no type
tag or terminal flag: the prompt says what is required, and the run's `status`
records the outcome (`done`, `blocked`, or `waiting`), which is what the
coordinator acts on. The terminal "write the stopping brief" step and the
operator-checkpoint steps are scheduled uses of these conditions; a *done*
brief's proof is the run's `proof` against its criterion, closing the loop.
Adapted from OpenAI's Symphony, which makes the accepted stop state explicit so
the agent actually reaches it.

## What stays

Identity is ULID + function — never a persona for a run. Control stays
cooperative (ADR-0011): the stop conditions are a contract emitters honour, like
ADR-0035's met-needs-proof invariant, not something the bus enforces. The state
envelope stays single-writer and observability-only. The records evolve by their
`$type` version tag, so this adds nothing to the locked core.

Map ([ADR-0003](0003-high-level-architecture.md)): the run and template records and the
coordinator are reference clients over Messages, Artifacts, and the goal
convention; the bus is unchanged.

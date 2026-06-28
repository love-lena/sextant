---
status: proposed
date: 2026-06-27
---

# The run executor drives a run by adopting the artifact the dash wrote

[ADR-0048](0048-a-run-is-one-live-instance.md) made a run one live instance of work,
held as a `sextant.workflow.run/v1` artifact. That envelope froze: the dash wrote it
and nothing advanced it. This ADR records the decision (TASK-236, folding TASK-225
checkpoint-approve and TASK-226 cancel) for the executor that drives a run to a
terminal status.

The executor is the existing coordinator client (the `sextant-workflow` managed
component) retargeted to the run/v1 contract — an ordinary bus client running the
engine as a library, not an engine in core. It is the **single writer** of the run
envelope. The dash writes the run **once** at spawn (its spawn act) and is read-only
after; it polls the envelope to render progress.

The wake is a hand-off, not a takeover. The dash, having written the run artifact,
publishes a `run.start{id}` on `msg.topic.run.start`; the coordinator **adopts** the
run by id, (re)owns it, and walks its steps. A run.start published while the
coordinator is briefly down is intentionally missed and re-issued — new-only
delivery, not a replayed durable queue (the TASK-192 anti-crash-loop discipline).

Steps run by **kind**: a `work` step dispatches an agent (compose the dispatcher —
publish `spawn.request`, await `spawn.ack`, await the agent's `run.event` step-done on
`msg.workflow.run.<id>.events`, attach any artifacts it reports); a `checkpoint` step
parks the run at `waiting` until the operator sends a cooperative `run.control`
approve/resume (or cancel) on `msg.workflow.run.<id>.control`; a `brief` step writes
the terminal stopping brief and is **gated** — the run may not go terminal without a
brief artifact attached, and the agent's reported outcome (`done`/`blocked`) becomes
the terminal status. A failed step drives the run to `blocked`; there is no `failed`
run status. Progress is the run's **embedded** activity stream, the low-volume
milestone log the dash polls — distinct from, and never sharing a channel with, the
high-volume per-agent `agent.activity` feed ([ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md)
amendment, TASK-235) whose `turn_end` is the worker-at-rest signal an output-gated
executor consumes.

Control is cooperative (ADR-0011 / ADR-0048): the coordinator acts only on the verb
the operator sends; the bus is unchanged, nothing here touches the locked core. Every
blocking wait is deadline-bounded and fails loud. The new records (`run.start`,
`run.event`, `run.control`, the run/template envelopes) are conventions over Messages
+ Artifacts, co-equal in Go and TS, evolving by `$type` version — no epoch bump.

The old `sextant.workflow/v1` path is retired in a following pass (TASK-234 engine
half); until then it remains compiled-but-unused so the change lands additively.

---
status: proposed
date: 2026-06-22
---

# A mobilized agent is a resumable one-shot function the bus wakes

> **Amendment (TASK-235, 2026-06-27):** the `pi.activity` stream referenced below was
> promoted to the harness-neutral **`agent.activity`** feed on `msg.agent.<id>.activity`
> (see [ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md)); read the
> `pi.activity` mentions as that feed.

A mobilized agent is a **durable identity plus a one-shot function**, not a process
you keep alive. Mobilizing it runs the function once: it takes its brief, does the
work, **reports**, and exits. A later message addressed to it wakes a fresh run that
**resumes its own session** — the agent literally continues the conversation it left
in durable storage, with no process idling in between. The identity and the session
persist; the process does not. This realises the bright line *call functions, never
manage processes or identities* for the dispatcher, and closes the gap that made the
"Mobilize" and "Start a workflow" surfaces ack and then go silent.

## What it builds on, and the gap it closes

[ADR-0033](0033-a-dispatcher-mints-its-own-workers.md) gave the dispatcher a worker
to stand up; [ADR-0011](0011-workflows.md) made a workflow step a trigger-then-emit
over Messages and Artifacts; [ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md)
made a headless `pi --rpc` session a first-class bus client with its own identity, a
persisted JSONL session, and a `pi.activity` stream the dash renders. All shipped.
What was missing is the **post-ack contract**, and the live dispatcher had drifted to
the wrong runtime to honour it: it was pointed at the `claude -p` recipe (`agent.sh`),
a one-shot that runs once and exits with no session to resume and its output written
to a discarded stream. So a mobilize minted a registered identity — the dash reported
"agent spawned · Message →" — over a process that had already exited and kept no
session, so a follow-up DM reached no one and could resume nothing. A workflow step's
agent was handed `WF_EVENTS=… WF_STEP=…` appended to its prompt but never told what
that meant, so it never emitted the done event and the coordinator timed out at ninety
seconds. The `spawn.ack` fired the instant the dispatcher *forked* — it meant "I
launched something," never "an agent is alive and will do the thing." That is the
confusing, invalid UX: an ack with nothing behind it, and no way to see why.

The fix is not to keep a process alive. A resident worker per agent — or the
`spawn-poc` supervisor wired by `--on-wake` — is exactly the process-management the
project refuses, and it leaves an idle fleet sitting on memory for conversations that
may never resume. The fix is to use the runtime that can be *re-entered*: a `pi --rpc`
worker whose session is keyed to the agent's bus id, so the dispatcher can run it,
let it exit, and re-run it later straight back into the same conversation.

## The model: report and exit; a message wakes the next run, resumed

Every invocation does three things and then exits. It **works from its brief and its
session** — a fresh mobilize starts a new session from the prompt; a revival resumes
the persisted JSONL keyed on the agent's id (`pi-<id>`), so the agent continues where
it left off. It **does the work**. And it **reports**: a result on its conversation (a
message, or an artifact it links by name) and, when it was woken by a workflow step,
the step-done event on that step's event subject. The report — not the ack — is what
"done" means and what surfaces; pi's `pi.activity` stream carries the run as it
happens, so a run that does nothing is a visible non-report rather than silence.

An agent is revived **by a message addressed to it**. This resolves the open
question — a per-artifact follow-up hook, or one universal rule — in favour of the
universal rule, because it is the primitive and not a policy: a message to a dormant
agent (a DM, or a post on a topic it owns) wakes a fresh run, resumed into its
session, with that message as the trigger. There is nothing to register per artifact
and nothing for the agent to keep running between messages.

The **dispatcher is the universal invoker**. Besides watching its spawn subject, it
holds standing subscriptions that wake any agent it minted on inbound to that agent's
wake drops — its `msg.client.<id>` inbox and its two 2-party DM topics
(`msg.topic.dm.<sorted ids>`), since a follow-up reply lands on the DM conversation,
not the inbox — re-running the `pi --rpc` harness under the agent's own credential and
resuming its session. The set of revivable agents it holds is in-process for this
first cut, so a dispatcher bounce orphans agents minted before it; re-deriving the set
from the registry (the agents whose `SpawnedBy` is this dispatcher) on restart is a
named follow-up below. Because the spawn subscription is deliver-new, a restart does
not replay the retained spawn history into a fresh fleet. A wake that arrives while a
run is in flight is coalesced — single owner per session id, as
[ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md)'s handoff already
requires — so a burst of messages does not fan out into overlapping runs against one
JSONL; the one window this cut does not yet close is a wake that races the wind-down
itself (after a run decides to drain, before the dispatcher marks the agent dormant) —
also a named follow-up. This is the wake-loop of [ADR-0033](0033-a-dispatcher-mints-its-own-workers.md)
folded into the one infrastructure process that already exists, instead of a process
per agent; the recursion fence is untouched, because a revival simply re-runs the
harness and a spawned worker still cannot mint.

The dash's "Message →" stops promising a live process and becomes what it now is:
**wake this agent for follow-up.** The operator's message drops on the agent's inbox,
the dispatcher revives a resumed run, and the agent answers on the conversation it
continues.

## Consequences

The dispatcher's reference harness becomes `pi --rpc` (`recipes/pi.sh`), and the
`claude -p` recipe (`agent.sh`) is set aside as the dispatcher's worker runtime — the
swappable-harness seam of [ADR-0033](0033-a-dispatcher-mints-its-own-workers.md) is
what makes the swap a one-line deployment change rather than a rewrite. `pi.sh` stops
holding the worker's stdin open for the life of the process; a run completes its turn,
reports, and exits, and the dispatcher — not a held-open FIFO — is what brings it back.
The dispatcher gains the standing wake subscriptions and an in-process revival set,
and dedups handled requests within its run (deliver-new is what keeps a restart from
replaying the spawn log); `--on-wake` and the standalone `spawn-poc` supervisor are
retired in its favour. The headless pi worker learns the workflow
report: when its brief carries `WF_EVENTS`/`WF_STEP`, it emits the step-done event the
coordinator waits on. The dash's mobilize and workflow views stop treating "a new
client appeared" as success and key their success and liveness on the agent's report
and `pi.activity`.

Two operational requirements move from incidental to load-bearing: the dispatcher's
environment must carry the pi worker's model credential (`ANTHROPIC_API_KEY`) and the
built `@sextant/pi-bus` extension path, since a `pi --rpc` worker runs a real model —
their absence is precisely why a pi-harnessed spawn would have failed silently too.

Idle cost is zero — no agent process exists between messages. Liveness is *identity in
the registry plus last report*, consistent with [ADR-0011](0011-workflows.md)'s
"liveness is presence-plus-staleness, not a heartbeat." Re-deriving the revival set
from the registry on restart so it survives a dispatcher bounce, closing the
wind-down wake race (a message that arrives after a run decides to drain but before
the dispatcher marks the agent dormant is currently dropped — deliver-new, no replay),
rate-limiting revivals, garbage-collecting dormant agent identities and their session
JSONL, and a worker
self-exiting on an idle timeout rather than at end-of-turn are named follow-ups, not
load-bearing for the first cut.

This refines the dispatcher lifecycle of
[ADR-0033](0033-a-dispatcher-mints-its-own-workers.md), adopts the pi worker of
[ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md) as its runtime, and
composes with the workflow coordinator of [ADR-0011](0011-workflows.md); it adds no
bus operation.

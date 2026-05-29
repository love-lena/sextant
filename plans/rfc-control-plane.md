# RFC: The sextant control plane — from process tracker to declarative orchestrator

**Status:** Draft for review
**Author:** Claude (with Lena)
**Date:** 2026-05-29
**Scope:** The daemon's authority over agent containers — how it *knows*
and *corrects* their state — plus the contract substrate that authority
rests on. Vision-first; the migration path is deliberately secondary.

---

## Narrative

An agent's container OOMs at 2am. The daemon notices, writes `lost` in its
record, and *stops there*. The agent sits idle until morning, when someone
runs `agents list`, sees it, and types `restart`. Nothing broke in the
daemon — it did exactly what it was built to do. It just wasn't built to
*act*.

That's the gap this RFC closes. Today `sextantd` is a careful **bookkeeper**
of agent state; we want a **control plane** — something that, given a
declaration of the agents you want, makes reality match and keeps it
matching. You stop issuing commands ("restart that container") and start
declaring intent ("this agent should be running"); one reconcile loop owns
the gap between intent and reality and closes it continuously — when a
container exits, when a spec drifts, when a sidecar falls behind a new
daemon. Recovery, drift, and version-skew stop being three problems and
become one: *convergence*.

We don't have to invent this. It's the Kubernetes control-loop pattern minus
the hyperscale machinery — closer in spirit to Fly's `flyd` than to a
cluster. We copy the discipline (declarative desired state, level-triggered
reconciliation, idempotent actions, one validated front door) and skip the
distribution (no Raft, no scheduler, no multi-host). The result: a small,
legible daemon where divergence is always transient and no two pieces of
truth can silently disagree — including the drift that produced this cycle's
two shipped bugs.

---

## TL;DR

`sextantd` holds a declarative desired-state record per agent and senses
liveness, but never **acts** — every automatic path ends in "mark `lost`,"
and only an operator mutates a container. This RFC closes the loop.

**The keystone is declarative agent state:** the operator writes *desired*
state; one level-triggered, idempotent reconciler is the **sole actuator**
(handlers write desired to KV; they never touch the runtime). That single
decision dissolves the spawn/restart drift class *by construction*, makes
recovery/drift/skew all *convergence*, and turns imperative verbs into
intent edits. Copy k8s's **state-management discipline, not its distribution
machinery** (blueprint: Fly's `flyd`; identity: "single-node kubelet").

The control-plane core — on top of a keystone refactor (lossless `restart`)
and a contract track that makes the wire + verbs un-driftable:

| Phase | Contents | Nature |
|-------|----------|--------|
| **P0 — the spine** | one idempotent, level-triggered, periodic + event-hinted reconcile; sole writer of observed state; spec/status split | restructuring existing pieces |
| **P1 — recovery** | auto-restart an involuntarily-lost agent — `RestartPolicy`, backoff, crash budget | a branch in the loop |
| **P2 — drift** | detect stale specs via a fingerprint; converge by restart | a second branch |

Plus two correctness fixes (a finalizer-shaped archive volume leak; NATS-authz enforcement of the sole-publisher rule). Full sequence in §11.

---

## 1. Motivation

`PRINCIPLES.md` asks us to treat ergonomic and reliability gaps as
correctness bugs. Two such gaps motivate this RFC, and both showed up as
real escapes this development cycle:

1. **Lost agents stay lost.** A container that OOMs, panics, or vanishes
   during a daemon outage gets marked `lost` and then *sits there*, doing
   nothing, until a human notices and types `restart`. For a tool whose
   whole job is to keep agents working, "we noticed it was lost and wrote
   that down" is not enough.

2. **Coupled truths drift silently.** We shipped two bugs this cycle from
   the same root cause — two code paths that must agree, kept in sync by
   memory: `restart` dropped a bind-mount that `spawn` sets (so a restarted
   agent's `agents context` broke), and the TUI menu launched surfaces with
   the wrong argv. The container-spec path still has **three more**
   spawn-only mounts that `restart` silently omits. Drift is the tax we pay
   for duplication, and nothing is collecting it.

Both are instances of one deeper fact: **sextant has the *parts* of a
control plane — a desired-state store, sensors, actuators, optimistic
concurrency — but they aren't wired into a loop, and the truths they
depend on aren't held in one place.** We don't need to invent the
closed-loop orchestrator; it's a well-trodden design. We need to assemble
the parts we already have into it, and copy the parts we're missing from
systems that have already paid for the lessons.

---

## 2. The vision — what sextant should be

You manage *agents*, declaratively. `spawn`/`stop`/`archive` become edits to
desired state; **editing a running agent's spec** (image, env, mounts) makes
the reconciler converge it. You never restart a container by hand — you
declare intent and the daemon keeps reality matching, backing a
crash-looping agent off and parking it loudly only when it has genuinely
given up. `sextant agents` shows a `RESTARTS` column and a last-exit reason,
like `kubectl`, because the daemon keeps that score.

When you ship a new daemon, agents converge to it: one whose sidecar is
behind is restarted onto the current image, not left speaking a stale
protocol. **Restart is the universal repair** — the same machinery that
recovers a lost agent upgrades a stale one — and it's safe because agent
state lives outside the container and restart rebuilds the *exact* spec
losslessly.

North star: *a small, legible, self-healing control plane where divergence
is always transient, every mutation is validated at one door, and no two
pieces of truth can silently disagree.*

---

## 3. Design principles

These constrain every decision below. They are ordered; earlier ones win.

1. **One declaration, many projections.** A fact is authored once, in one
   authoritative form; every other representation is *derived* from it,
   never re-typed by a human. The enforcement ladder, strict and in order:
   **generate** (a tool emits the copy — for cross-language/cross-process
   boundaries) › **single-source** (one function/value every path calls —
   for in-process agreement) › **contract-test** (a test asserts two ends
   agree — the backstop, only where neither of the above reaches). "Leave
   it to memory" is not a rung.

2. **Level-triggered, not edge-triggered.** Events (a docker `die`, a
   heartbeat tick) are *hints that something might have changed* — never
   the payload of what to do. The reconcile body re-reads the full desired
   record and re-observes actual reality from scratch, every time. A
   dropped, duplicated, or reordered event then only *delays* convergence
   to the next tick; it can never permanently desync us. This is the single
   highest-leverage idea in the document.

3. **Converge by restart; defer skew tolerance.** We are single-host,
   single-operator, with agent state persisted outside the container — so
   we *can* restart anything cheaply, and therefore we do. Mismatched truth
   (a lost agent, a stale sidecar, a drifted spec) is fixed by
   restarting/upgrading the thing that's behind, not by teaching components
   to interoperate across versions. Skew tolerance is an explicit,
   dated non-goal with a tripwire (§7).

4. **One validated front door.** Every state mutation passes through the
   daemon, and the daemon is the *only* principal the broker permits to
   issue agent commands. Reads may bypass it; writes may not. This is
   k8s's "API server + admission" and it is enforced structurally, not by
   convention.

5. **Keep the sidecar; defang it.** The agent's brain runs *in* its
   container, co-located with its hands and filesystem, inside the
   isolation boundary. We do not move the SDK into the daemon (§5.5). We
   remove what makes the sidecar *painful* — uncontrolled version skew —
   by bringing it under principles 3 and 4.

6. **Right-sized.** Copy k8s's state-management discipline; skip its
   distribution machinery. Every "we won't build X" in §6 is a scale
   problem we don't have.

---

## 4. Where we are today (grounded)

The daemon already has the *parts*. Mapped onto the control-loop triad
**observe → diff → act**:

**Observe (strong).**

- **Desired-state store.** `AgentDefinition` in NATS KV
  `agent_definitions.<uuid>` (`pkg/sextantproto/agent.go`) — the full
  intended spec: image, mounts, env, runtime, tools, `CurrentIncarnationID`,
  `Lifecycle`, `Version`.
- **State machine.** Lifecycle `defined / running / paused / archived /
  ended / crashed / lost`; incarnation `starting / ready / exited /
  failed`. Guarded by `CurrentIncarnationID` (a generation/fencing
  counter) + JetStream KV CAS (`Update(key, raw, revision)`;
  `ErrKeyExists` is literally a 409).
- **Three sensors.** L1 heartbeat cache (`agents.*.heartbeat`, 30s
  staleness, in-memory, *no version field*); L3 docker `die`-event
  watcher (5s debounce so it doesn't race a clean sidecar shutdown); L2
  startup reconciler (one-shot, present/absent diff of KV-running vs
  docker-by-label).

**Diff (shallow).** L2 checks *existence only* — is there a live container
for each running definition? It never `Inspect()`s a container's actual
image/mounts/env. Spec drift is invisible. And it runs **once, at
startup** — there is no periodic loop.

**Act (almost nothing).** Every automatic path terminates in the same
place: publish a synthetic `lost` envelope → the lifecycle watcher flips
KV `Lifecycle` to `lost`. That is **updating a label, not correcting
reality.** The only things that mutate a container are operator RPCs:
`spawn` (create+run), `restart` (stop old incarnation, run new — the
re-create primitive), `kill` (current verb; stops the agent → `defined`), `archive` (stop →
`archived`, release name, clean volume).

**The sidecar + data path (for context).** Each agent runs one
`sextant-sidecar:latest` container that *is* the Claude Agent SDK loop
(`images/sidecar/entrypoint/`); tools execute locally in the container;
it talks NATS (`agents.<uuid>.{inbox,frames,heartbeat,lifecycle}`). The
daemon already receives a live structured frame stream. `agents chat`
sends prompts **through** the daemon (RPC `prompt_agent` → daemon
publishes to inbox) but receives frames **directly** from NATS, bypassing
the daemon — which is correct (broker efficiency; no data-plane SPOF).
The catch: the operator credential has unrestricted NATS perms
(`publish: ">"`), so the prompt gate is currently *conventional*, not
enforced.

The summary: **a tracker with the actuators and the desired store both
present, but nothing wiring "I noticed it's wrong" → "I fixed it," and
several truths held in more than one place.**

---

## 5. Target architecture

**The commitment: declarative agent state.** Everything below rests on one
decision — the operator declares *desired* state and the reconciler is the
**sole actuator**. Handlers no longer run or stop containers; they
*validate and write desired state*, and the reconcile loop is the only code
that touches the container runtime. This is stronger than "one front door
for commands" (§5.7) — it's one place where *actions happen*. Three
consequences make it the trunk the rest of this section branches off:

- **The spawn/restart drift class dissolves by construction.** With a sole
  actuator there is exactly *one* path that builds-and-runs a container,
  fired whenever `observed ≠ desired` — nothing to keep in sync. The
  single-source builder (§5.4) still exists, but goes from two callers we
  must align to **one**. The #50 bug stops being *possible*, not just
  fixed.
- **Loss, drift, and epoch-skew become one thing.** Not three features —
  three *divergences* the loop closes (`observed=lost`,
  `fingerprint≠desired`, `epoch<current`). Convergence is the only verb;
  P1/P2 are `if` branches, not subsystems.
- **Imperative verbs become intent edits.** `spawn` → write `desired=run`;
  `stop` → `desired=stopped`; `archive` → `desired=archived` (idempotent
  for free). The resource-verb CLI stays as ergonomic sugar; only its guts
  change — and this *aligns with the queued verb renames* (`spawn→create`,
  `kill→stop` read perfectly as desired-state edits). `restart` has no
  natural desired-state, so it becomes a **re-actuation nonce** on the
  record (k8s `rollout restart`-style: bump it → reconciler rebuilds the
  incarnation), not an imperative escape hatch.

This is the highest-value, highest-blast-radius change in the milestone —
handlers that today call `Containers.Run/Stop` stop doing so and just write
KV — so the existing 3-layer lifecycle's race-hardening must be *carried
into* the reconciler, not lost.

### 5.1 The spine — one reconcile loop

Replace the one-shot, edge-triggered "mark lost" machinery with a single
**reconcile function** that is the heart of the control plane:

```
reconcile(agentID) -> (nextCheckAfter, error)
    def    := read AgentDefinition from KV            // desired (intent)
    actual := inspect docker for this agent           // observed (truth)
    decide what (if anything) makes actual match def.desired
    write observed status back (sole writer)
    return a requeue hint
```

Properties, each load-bearing (§3.2):

- **Level-triggered.** Re-reads desired and re-observes actual *every*
  call. Takes only an agent *identity* as input — never "what changed."
- **Idempotent.** "Ensure running," not "start." Safe to run twice; a
  double-fire is a no-op, a missed fire is recovered next tick.
- **Single writer of observed state.** L1/L3/startup become *hint
  sources* that enqueue a reconcile; they no longer write status
  themselves. Today three code paths can mutate an agent's observed state
  — that is a multi-writer race *inside one process* (the same class of
  bug as our subagent-checkout clobbering). One writer eliminates it; CAS
  protects the bytes, single-writer protects the logic.
- **Periodic + event-hinted.** A timer enqueues a full reconcile of every
  agent every **30–60s** (the *backstop* that catches deaths the docker
  watcher missed — event streams drop events; a periodic level reconcile
  makes us eventually-consistent regardless). Docker events and heartbeat
  staleness are *additional* hints that enqueue sooner. On
  watcher-reconnect, force one immediate full reconcile (our equivalent of
  k8s's post-`410 Gone` relist).
- **Backpressured.** A keyed, de-duplicating, single-in-flight-per-agent
  work queue feeds the reconcile; start with **one worker** (serialized
  reconcile avoids racing the docker socket and is simpler). Failed
  actions requeue with **exponential backoff** (§8); routine re-checks
  use a flat `RequeueAfter`-style delay. Keep those two requeue paths
  distinct — routing a failed restart through the periodic path defeats
  the storm guard; routing a poll through the backoff path makes the poll
  interval mysteriously balloon.

### 5.2 spec/status split

`AgentDefinition.Lifecycle` today conflates **intent** (`paused`,
`archived` — an operator asked for this) with **observation** (`crashed`,
`lost` — the reconciler discovered this). One field, two writers, is the
read-modify-write clobber the k8s API conventions exist to prevent.

Split them:

- **desired** (operator-written, reconciler read-only): `run` / `paused` /
  `archived`. Expresses intent.
- **observed** (reconciler-written, operator read-only): `pending` /
  `running` / `crashed` / `lost` / `ended`, plus `restartCount`,
  `lastExitReason`, `phase`.

The reconciler's entire job then reduces to one sentence: **"for every
agent where `desired=run` but `observed≠running-and-healthy`, act."** We
do not need k8s subresources or RBAC — just the field split and a writer
discipline. Guardrail: a status write must **not** itself trigger a
reconcile (watch intent, filter status-only changes) or you get an
infinite loop. The concrete target record — every field, spec vs status —
is in **Appendix C**.

### 5.3 RestartPolicy + the recovery branch

Give `AgentDefinition` a per-agent **`RestartPolicy`**: `Always` /
`OnFailure` / `Never`, default `OnFailure` (exit 0 = success, not
restarted; nonzero / killed = failure, restarted). Per-agent from day one
— k8s only retrofitted per-container restart policy in v1.34 and visibly
wishes it hadn't waited; an agent ≈ one container, so we model it at the
container grain.

The recovery branch is then trivial inside the spine: `desired=run ∧
observed∈{lost,crashed} ∧ RestartPolicy≠Never ∧ under crash budget` →
restart (via the lossless single-source builder, §5.4). All the safety —
backoff, budget, terminal park — lives in the reconcile (§8). Recovery is
*safe* to automate because a lost agent has nothing to interrupt; this is
why it comes before drift.

### 5.4 Restart as a pure projection (the lossless gate)

Because restart is now the universal repair *and* the upgrade path, it
**must** rebuild the exact intended spec. A lossy restart under
auto-restart doesn't drop one mount — it *automates the propagation* of
that drift on every crash.

So: the container spec becomes a **pure projection of the persisted
`AgentDefinition`**, built by one `buildAgentContainerSpec(def)` with a
**single caller — the reconciler** (per §5's commitment). Under declarative
state there is no separate spawn-actuation and restart-actuation to keep
aligned; there is one actuation path, so the drift class is gone *by
construction*, not merely de-duplicated by a shared helper. The builder is
still the lossless projection — the **hard prerequisite for P1** (auto-restart
on a lossy build automates drift) — and it retires the three latent
spawn-only mounts on the way.

### 5.5 The sidecar — kept, brought under the principles

We keep the sidecar. Moving the SDK into the daemon would invert isolation
(the SDK runs its tools where it runs — in the daemon's FS/shell unless we
proxy every tool and file op across a boundary the SDK has no seam for),
make the daemon a true SPOF for all agents, and fight the SDK's grain. The
data-plane bypass for frames is *correct* and stays.

What changes is that the sidecar comes under principles 3 and 4:

- **Converged by restart.** It's already one image and reports
  `SIDECAR_VERSION` — but the heartbeat payload doesn't yet carry it. Add
  it, and the reconciler treats "running an out-of-epoch sidecar" as drift
  (§5.6, P2): restart onto the current image. The sidecar stops being a
  skew source because it's never allowed to be stale.
- **Scoped at the door.** Its per-incarnation JWT (already minted at
  spawn) is narrowed to `agents.<uuid>.*` only (§5.7).

The MCP "thin sandbox" alternative (brain in daemon, container as a dumb
MCP executor) is the only architecturally-clean way to relocate the
brain, and is explicitly deferred (§7) — it keeps a per-container process,
keeps the SPOF, and adds a round-trip per tool call, so it isn't worth it
at current scale.

### 5.6 Drift — the spec fingerprint

To detect a container running the *wrong* spec without false-positives
(docker normalizes/injects mounts and env; `ContainerInfo` doesn't even
expose them today), do **not** deep-compare the live spec. Instead:

- At build time, hash the inputs we control (image ref + our explicit
  mounts + env keys + `SIDECAR_VERSION`) into a **spec fingerprint**, and
  stamp it as a container label.
- To detect drift, recompute the desired fingerprint from the
  `AgentDefinition` (via the same `buildAgentContainerSpec`) and compare to
  the stamped label. Mismatch ⇒ built from a stale spec ⇒ converge by
  restart.

This sidesteps both the normalization false-positives and the
missing-inspect-fields problem, and the fingerprint is just a hash of the
single-source builder's output — it falls out of §5.4 for free.

Drift is *delicate* to act on (a drifted agent is often healthy and
mid-work), so drift-correction restarts at a **turn boundary** (the sidecar
already emits `lifecycle.turn_ended`), not mid-turn — the drain policy
recovery doesn't need. The thorough vision (§2) lets operators **edit a live
agent's spec** and have the reconciler converge it, so a
`desiredGeneration`/`observedGeneration` pair is **in**: the reconciler
stamps `observed` once it has applied the latest edit, which is how "has it
caught up?" becomes answerable. Do **not** overload `CurrentIncarnationID`
for this — run-identity and spec-version are different questions.

### 5.7 The front door — sole publisher, enforced

The daemon becomes the **only** principal the broker permits to publish to
`agents.*.inbox`. Split today's single unrestricted account into
role-scoped principals:

| Principal | may publish | may subscribe |
|---|---|---|
| **daemon** | `>` (incl. `agents.*.inbox`) | `>` |
| **operator CLI** | `sextant.rpc.*` requests only — *not* inboxes | `agents.*.frames`, `agents.*.lifecycle`, RPC replies |
| **sidecar** (scoped per-uuid via its JWT) | `agents.<uuid>.{frames,heartbeat,lifecycle}` | `agents.<uuid>.inbox` |

This is k8s's "every mutation goes through one validated front door,"
made structural: the prompt gate stops being a politeness clients observe
and becomes a rule the broker enforces. Payoffs: the audit becomes the
*system of record* (every command provably transited the daemon, because
there's no other door), and the daemon owns command sequencing/idempotency
for real. Pair it with a shared **`decode → default → validate`** pre-step
in front of every handler (mutating-then-validating admission), so
defaulting and validation stop being re-inlined per-handler and drifting
between `archive` and `stop`. Reads stay off the gauntlet (the TUIs
reading KV directly are fine — latency for no safety gain).

### 5.8 Contract substrate (the parent thread, folded in)

The control plane rests on contracts that can't drift (§3.1). The
load-bearing pieces:

- **Wire pipeline.** `payloads.go` → `schemas/*.json` is already
  generated; extend the generator to emit the **TS types + a generated
  `proto_version`**, so the Go↔TS sidecar/client wire (envelopes, frame
  kinds, `PROTO_VERSION`) is generated, not hand-synced. The sidecar's
  NATS protocol is the *same kind* of Go↔TS contract as the CLI's — it is
  the second consumer of the generated types, and the strongest argument
  for generating them.
- **`VerbSpec` table.** Collapse the parallel lists (verb consts, `CapFor`,
  `rpc.go` registration, the generator's hand-maintained type enumeration)
  into one declarative table `{name, capability, handlerFactory, req,
  resp, phase}`. Dispatch *iterates* it, `CapFor` *reads* it, schema-gen
  *walks its types*. You cannot add a verb and forget its handler,
  capability, or schema, because there's one entry. (`phase` preserves the
  existing two-stage registration — initial vs lifecycle verbs.)
- **Convergence handshake — the epoch.** A single generated integer,
  `WireEpoch`, emitted from the schema into *both* Go and TS from one
  source (no hand-sync); `ProtoVersion` stays a cosmetic/derived string.
  `WireEpoch` is the machine-checked compatibility key, and it is detected
  at **three moments**:
  - *Authoring (the crux):* a **CI schema-compat gate** diffs the
    regenerated schemas against the committed ones and **fails the build
    if a breaking change lands without an epoch bump** — breaking = a peer
    on the old schema would misread/reject the new wire (removed/renamed
    field, type change, optional→required, removed enum value); additive =
    non-breaking; ambiguity → bump. The bump is *read off the diff, not
    remembered* — the wire analog of our changelog-bump rule, and fully
    mechanizable because the schema is machine-readable (cf. `buf
    breaking`). You cannot merge a breaking wire change without the epoch
    moving.
  - *Stale agent:* the reconciler compares each container's `wire_epoch`
    **label** (stamped at spawn; the sidecar also reports its live epoch in
    the heartbeat as cross-check) against the daemon's `WireEpoch`. `label
    < daemon` is drift (§5.6) → **converge by restart** at a turn boundary.
    The label lets this work even for an *exited* container (no live
    process needed); epoch is one component of the §5.6 fingerprint.
  - *Stale peer:* the admission front door (§5.7) checks the epoch in every
    RPC envelope; a mismatch is **rejected with a diagnostic**
    (`make install`) because the daemon can't restart the CLI.

  Three regimes, one key: agents **converge by restart**, the CLI **fails
  fast**, and stateful stores (ClickHouse, NATS) **migrate forward** on
  startup.

We already have, correctly and under different names: `resourceVersion`
(KV CAS) and `generation` (incarnation-id). The one fix: **reconciler
writes retry-rebase on conflict; only operator RPCs surface the 409 to a
human.** A background loop that bails on conflict isn't a reconciler.

### 5.9 Verifiability — a property the design buys us

This work began as a testing-SOP effort, and the architecture above is
chosen partly *because* it is cheap to verify. A level-triggered,
idempotent, single-writer reconcile is dramatically more testable than the
edge-triggered "mark on event" code it replaces:

- **Convergence is a unit test.** Inject a desired record + a fake observed
  state (fake docker), run `reconcile` once, assert the action — no real
  containers, no wall-clock.
- **Resilience is a property, not a fixture.** Because events are only
  hints, "we dropped a `die` event" is tested by simply *not* delivering it
  and asserting the next periodic reconcile still converges — the exact
  scenario that's untestable in an edge-triggered design.
- **The crash-loop guard is deterministic** under an injected clock: drive
  N failures, assert the backoff schedule and the budget→terminal flip.
- **Idempotency is its own oracle.** Run any reconcile twice; the second
  call must be a no-op. That one assertion catches a whole class of
  double-act bugs.
- **The contract layer carries its own backstop** (§3.1): where we can't
  generate or single-source, a conformance test asserts both ends agree
  against the *one* declaration — the Tier-3 floor of the SOP this design
  feeds back into.

The properties that make the loop *correct* (level-trigger, idempotence,
single-writer) are the same ones that make it *testable*. That isn't a
coincidence — it's why we chose them.

### 5.10 The session record — frames live, JSONL on-demand backup

Resolves §9.2. The SDK writes its session `.jsonl` inside the container no
matter what; the *continuous bind-mount* of that file to the host is a
separate choice — and it's the one that drifted (the very mount `restart`
had to learn to re-apply in the #50 fix, and a thing both `spawn` and
`restart` must remember). Split the two jobs the mount was doing:

- **Live view (primary):** `agents context` reads the **frame stream** —
  daemon-owned, already flowing, no mount; `--follow` tails frames.
- **Authoritative backup (on demand):** the `.jsonl` stays the
  ground-truth record of *exactly* what the context was — never
  reconstructed from frames — but is **read on demand, not streamed**. We
  **remove the persistent `claude-projects` bind-mount** and fetch the file
  only when asked (`agents context --raw` / `--backup`), reusing the
  existing `read_file` / `exec_in_container` facility. No mount ⇒ that
  drift instance is gone and `resolveSessionJSONLPath`'s host-dir walk
  retires. (C0's single-source builder is still needed for the *functional*
  mounts — gitconfig, SSH, git-dir; this removes only the observability
  mount.)

The wrinkle: exec-read works only while the container is alive. For the
post-mortem backup (agent crashed — show me its exact final context), the
**reconciler captures a one-shot snapshot** of the `.jsonl` into the
agent's data dir when it observes the agent leave `running`, before any
removal — one copy in the loop we're already building, not a continuous
mount nor a hook scattered across stop/archive/restart. (Impl note: exec
needs a *running* container, so the exited-container case needs a
copy-from-container capability; a hard external `docker rm` before the
snapshot loses the JSONL — acceptable, since the durable record of *what
happened* is the persisted frame stream.)

**Sub-decision — RESOLVED: durable snapshot-on-stop.** The reconciler
copies the `.jsonl` to the agent's data dir when it observes the agent
leave `running`, so the backup survives the container.

---

## 6. What we are deliberately NOT building

Each solves a scale problem we don't have. Naming them is part of the
design.

- **Leader election / Raft.** One daemon — no election. And CAS is the
  *real* fencing anyway (k8s's own Lease docs admit leader election isn't
  fencing). Revisit only at multi-host (§7).
- **Sharded / horizontally-scaled controllers.** A handful of agents on
  one box; one worker goroutine is plenty.
- **The full informer / watch-cache / `resourceVersion` / bookmark
  stack.** We read straight from our local store + docker; no reflector,
  no delta FIFO, no relist protocol. (We *do* copy the local-cache
  *discipline* — keep an in-process model, refresh on hint + tick.)
- **CRDs / API aggregation.** We own all the types; they're Go structs.
- **A separate scheduler / placement engine.** One host = placement is
  "here." We don't even need a `pending` state (Fly carved the scheduler
  out for exactly this reason).
- **CNI / multi-node networking / service mesh / NetworkPolicy / RBAC /
  multi-tenancy.** One host, one trust domain, NATS subjects.
- **The global token-bucket rate limiter.** Replace with **startup jitter
  + a small concurrency cap**. The token bucket exists to protect a shared
  API server from 10k objects; we have tens of agents.

---

## 7. Deferred, with tripwires

Captured so they're not lost; explicitly out of scope now.

- **Capability-brokered observation.** When agents begin subscribing to
  *other* agents, do not grant ambient subscribe perms — have the agent
  *request* data via a control-plane verb and the daemon (as principal)
  wire the granted flow into the agent's inbox. The read-side symmetric
  analog of §5.7. Fork to decide then: daemon **republishes/filters into
  the inbox** (data-path, content control) vs **grants a scoped, revocable
  subscription** (access control only). **Trigger: first agent-to-agent
  observation.**
- **Subscribe-side authz.** The operator CLI's direct frame subscribe is
  fine now. Gate it when the above lands. **Trigger: same.**
- **MCP thin-sandbox.** Brain-in-daemon via an in-container MCP executor.
  **Trigger: going serverless / multi-tenant, or per-agent Node overhead
  becomes a real cost.**
- **Multi-host.** The day we genuinely run more than one host is the day
  leader election (15s/10s/2s lease ratio), the informer stack, and skew
  *tolerance* come back on the table. **Trigger: a second host, or
  external API consumers we can't restart.**
- **`restartPolicyRules` (exit-code-keyed restart).** Still alpha in k8s;
  over-build for v1. **Trigger: agents start signaling
  retriable-vs-fatal via exit codes.**

---

## 8. Concrete parameters (k8s-calibrated, agent-adjusted)

The algorithm is copied verbatim in *structure*; the constants track
k8s's *current stable* defaults (not its experimental 1ms-base path,
which is tuned for cheap web pods — a 1s-flapping agent would hammer the
model API and docker).

- **Restart backoff:** initial **10s**, ×2 → `10 → 20 → 40 → 80 → 160 →
  300`, **cap 300s**. No jitter on the per-item backoff (jitter is a
  multi-node concern); *do* jitter the startup thundering-herd.
- **Backoff reset:** an agent must run **continuously for 10 min** before
  its backoff counter resets — and this is an **independent constant, NOT
  "2× the cap"** (KEP-4603's own evolution proves coupling them is a
  trap). Reset only after a *stable* run (≥30s), or an agent whose
  container exits right after start resets its budget every loop.
- **Crash budget:** **5 restarts in 10 min → terminal `crashed`
  (CrashLoopBackOff), stop auto-restarting, surface to operator.** With the
  10s×2 sequence, 5 restarts ≈ 5 min, so a real crash-looper trips it well
  inside one reset window while a single transient crash never does. Keep
  a **monotonic lifetime `restartCount`** (operator visibility) *and* a
  separate **windowed counter** (the budget).
- **Grace:** SIGTERM → **30s** → SIGKILL, per-agent overridable, wired
  straight through `docker stop -t <grace>`. Bump default if agents do
  long checkpoint-on-shutdown; keep 30s baseline so a hung agent can't
  block daemon shutdown.
- **Reconcile cadence:** full sweep every **30–60s**; **1** reconcile
  worker to start.
- **Liveness (in P1):** a wedged-but-still-running agent (process alive, hung)
  is real and docker `die` never catches it — a periodic health check, **3
  consecutive failures / 10s period** → normal restart path. Skip
  readiness/startup probes (no traffic routing yet).

---

## 9. Decisions (now resolved)

All resolved as of this revision (kept for the rationale trail). The
governing call: **ship the complete single-host control plane as one
committed milestone**, with each fork set to its most thorough option —
bounded at the single-host scope line (§7 deferrals stay deferred).

1. **Epoch vs exact-match — RESOLVED: epoch** (§5.8). Detected at three
   moments: a CI schema-compat gate *enforces* the bump (read off the
   schema diff, not remembered); the reconciler label-compares to catch
   stale agents (→ restart); the front door checks the envelope epoch to
   catch stale peers (→ fail-fast diagnostic).
2. **Session JSONL backup — RESOLVED (§5.10).** Keep the `.jsonl` as the
   *authoritative* on-demand backup (never reconstructed from frames);
   **remove the persistent bind-mount** and read it on demand; frames cover
   the live view; the reconciler takes a **durable snapshot-on-stop** so
   the backup survives the container.
3. **spec/status split — RESOLVED: full split in P0.** It's the data model
   the entire loop diffs against; the heuristic discriminator would be a
   shim we'd rip out — doing the core twice is the opposite of thorough.
4. **Liveness — RESOLVED: in P1.** "Alive but wedged" (hung on a model
   call, deadlocked) is a real, common agent failure `docker die` never
   catches; a thorough enforcer must see it. 3 consecutive failures / 10s
   period → restart path (§8).
5. **Live spec edits + `observedGeneration` — RESOLVED: in.** The vision
   (§2) promises editing a record converges the agent; the thorough version
   commits to it, making `observedGeneration` load-bearing, not optional
   (§5.6).

---

## 10. Findings to file now (independent of the phases)

Two are concrete latent bugs of the exact shape we've been fixing; file
them as tickets regardless of when the phases land.

1. **Finalizer-shaped volume leak in archive.** `archive.go` flips the
   record to `archived` and *then* does a best-effort volume remove whose
   failure is only logged — so a failed cleanup leaves a "reclaimed"
   record with a leaked volume, silently. Fix: an intermediate `archiving`
   state, reclaim the volume *before* the terminal flip, reconciler retries
   stuck `archiving`. (k8s finalizer invariant: don't call it gone until
   cleanup is confirmed.)
2. **The front door is a convention, not a guarantee.** The sole-publisher
   decision (§5.7) is only real once NATS authz forbids the side door. File
   the authz-scoping work even if the rest of the front-door admission
   refactor waits.
3. **Three latent spawn-only mounts in `restart`** (`gitconfig`, SSH,
   git-dir) — fixed implicitly by §5.4, but file so it's tracked if §5.4
   slips.

---

## 11. The path (secondary)

This ships as **one committed milestone**, not a menu of optional phases —
we treat the complete single-host control plane as a single improvement.
The sequence is **dependency-ordered, not value-staged**: every row is in
scope; the order only reflects what must precede what. It still lands as
individually-correct, CI-green PRs that each leave `main` working — "ship
together" means one release that delivers the whole vision, not one
unreviewable diff. §5.9's testability is what makes a landing this size
safe; the one real risk is that the spine **replaces** the battle-tested
3-layer lifecycle, so each step must *preserve* its race-hardening
(incarnation CAS, sidecar-terminal precedence, the debounce) as it absorbs
it — not re-litigate it.

Every stage carries an **aggressive acceptance bar**: an **e2e test** against
a real daemon + containers, an **accumulating regression suite** (each
stage's regressions run in every later stage's CI), and an explicit
**expected-breakage declaration** naming any behavior a later ticket
restores. Declared breakage between stages is fine; *undeclared* breakage is
not. The standard + per-stage criteria live in the milestone tracker,
`plans/issues/feat-control-plane-milestone.md`.

| Step | Delivers | Depends on | Gate |
|------|----------|-----------|------|
| **C0** | `buildAgentContainerSpec` single-source projection (spawn+restart) | — | lossless restart verified (spawn ≡ restart spec, modulo identity) |
| **C1** | Wire pipeline: generate TS types + `proto_version` from schema | — | Go↔TS no longer hand-synced |
| **C2** | `VerbSpec` table; dispatch/CapFor/schema-gen iterate it | C1 | one verb entry, nothing to forget |
| **S0** | Remove `claude-projects` bind-mount; `agents context` live→frames, backup→on-demand read (+ reconciler snapshot-on-stop) | C0 (mount leaves the builder), P0 (snapshot in the loop) | no bind-mount; backup readable on demand and after the agent stops |
| **P0** | The reconcile spine: level-triggered, periodic + hinted, single-writer; **full spec/status split**; **reconciler = sole actuator** (handlers write desired, never touch the runtime) | — (parallel to C*) | L1/L3/startup only *enqueue*; one writer of status; no handler calls `Containers.Run/Stop` |
| **P1** | Recovery: `RestartPolicy` + auto-restart + backoff + crash budget | **C0**, P0 | crash-loop guard proven; budget→terminal park works |
| **P2** | Drift: spec fingerprint + converge-by-restart at turn boundary | C0, P0, P1 | no false-positive restarts; healthy agents undisturbed mid-turn |
| **F0** | Front-door NATS authz (sole publisher, enforced) + shared admission pre-step | — | non-daemon credential *cannot* publish to inbox |
| **Fixes** | Archive `archiving` state (volume-leak) | P0 (reconciler retries) | no silent volume leak |

C0 is the keystone: it's the lossless-restart gate for P1, the fingerprint
source for P2, and the fix for the latent mounts — do it first.

---

## Appendix A — the k8s homework digest

COPY/ADAPT/SKIP, with the failure each pattern prevents. Full sourcing in
the research thread; primary sources are k8s docs, client-go source,
KEP-4603, the controller-runtime/kubebuilder book, and the engineering
writeups of Nomad, SwarmKit, systemd, and Fly's `flyd`.

| Pattern | Verdict | Why / what it prevents |
|---|---|---|
| Declarative desired state | **COPY** (have it) | imperative commands have no memory of intent; can't self-heal |
| Level-triggered reconcile (events=hints) | **COPY** | a dropped/dup event permanently desyncs an edge system |
| Periodic resync loop | **COPY** | the backstop for missed events → eventually consistent |
| Idempotent reconcile | **COPY** | safe retry/replay; "ensure" not "do" |
| Single writer per resource | **COPY (in spirit)** | CAS protects bytes; single-writer protects logic |
| spec/status split | **ADAPT** | read-modify-write clobber between operator and reconciler |
| restartPolicy (per-agent) | **ADAPT** | intent-aware restart; don't restart a clean exit |
| CrashLoopBackOff + reset | **COPY shape, ADAPT constants** | restart storms; coupling reset to cap is a trap |
| Crash budget → terminal | **ADD** (k8s has no native equiv) | give up loudly instead of looping forever |
| Graceful SIGTERM→grace→SIGKILL | **COPY shape, ADAPT 30s** | let an agent checkpoint before the container is force-killed |
| Single front door + admission | **COPY hard** | one enforce/default/audit point; structural, not convention |
| Optimistic concurrency (resourceVersion) | **COPY** (have it: KV CAS) | lost updates under concurrent writers |
| generation / observedGeneration | **ADAPT** (have generation: incarnation) | "has the reconciler caught up to the latest spec?" |
| Finalizers | **ADAPT** | orphaned external resources on delete (our volume leak) |
| Owner refs + cascading GC | **ADAPT concept, SKIP machinery** | centralize the cascade; skip generic reachability GC |
| Leader election / Lease | **SKIP** | single host; CAS is the real fencing |
| Informer/watch-cache, CRDs, scheduler, CNI, token bucket, sharding | **SKIP** | hyperscale/HA/multi-tenant problems we don't have |

**Foot-guns to encode as guardrails:** edge-triggered drift ·
non-idempotent reconcile · no-backoff storms · spec/status conflation ·
status-write-triggers-reconcile infinite loop · thundering herd on
startup · one-giant-reconciler · authoritative state in process memory.

**Key sources:** [k8s controller concept](https://kubernetes.io/docs/concepts/architecture/controller/) · [API conventions — level-triggering + spec/status](https://github.com/kubernetes/community/blob/main/contributors/devel/sig-architecture/api-conventions.md) · [Level Triggering & Reconciliation](https://medium.com/hackernoon/level-triggering-and-reconciliation-in-kubernetes-1f17fe30333d) · [KEP-4603 — Tune CrashLoopBackOff](https://github.com/kubernetes/enhancements/blob/master/keps/sig-node/4603-tune-crashloopbackoff/README.md) · [client-go default rate limiters](https://github.com/kubernetes/client-go/blob/master/util/workqueue/default_rate_limiters.go) · [Fly — Carving the Scheduler Out of Our Orchestrator](https://fly.io/blog/carving-the-scheduler-out-of-our-orchestrator/) · systemd `Restart=` / `StartLimitBurst`.

---

## Appendix B — decision log (this design thread)

1. **Keep the sidecar; don't move the SDK into the daemon.** Isolation
   inversion + SPOF + fights the SDK grain. Defang via §5.5 instead.
2. **The data-plane frame bypass is correct** and stays; only the
   command path is gated.
3. **Daemon = sole publisher to agent inboxes**, enforced via NATS authz
   (§5.7). Subscribe-side gating deferred (§7).
4. **Converge by restart; defer skew tolerance** (§3.3). Restart is the
   upgrade primitive ⇒ restart must be a lossless projection (§5.4).
5. **Enforcer order corrected to spine → recovery → drift** — "cadence" is
   the spine the other two are features of, not a middle step.
6. **Wire/proto pipeline + `VerbSpec` together** as the contract
   centerpiece (§5.8).
7. **Copy k8s discipline, skip its distribution** (§6); blueprint is
   `flyd`, identity is "single-node kubelet."
8. **Session JSONL = authoritative on-demand backup, not a live mount**
   (§5.10). Remove the bind-mount; frames are the live view; the `.jsonl`
   is read on demand (and snapshotted on stop). Kills the #49/#50 mount
   class at the root.
9. **Ship the complete single-host control plane as one milestone**, every
   fork at its most thorough: full spec/status split in P0, liveness in P1,
   live spec edits + `observedGeneration` in. Thoroughness is bounded by
   the scope line — §7 deferrals stay deferred (their triggers haven't
   fired); thorough ≠ building untriggered features. Lands as
   dependency-ordered, individually-correct PRs, cut as one release.
10. **Declarative agent state is the keystone** (§5 lead). Operator declares
    *desired*; the reconciler is the **sole actuator** (handlers write
    desired, never touch the runtime). Dissolves the spawn/restart drift
    class by construction, unifies loss/drift/epoch as convergence, makes
    imperative verbs into intent edits (`restart` = a re-actuation nonce,
    k8s `rollout restart`-style). Highest value, highest blast radius; the trunk the
    milestone branches off.

---

## Appendix C — the declarative agent record (target schema)

The single viewable shape of an agent's declarative state — the answer to
"what *is* the file format." This is the **target** after the spec/status
split (§5.2); *today* it's the flatter `AgentDefinition` in
`pkg/sextantproto/agent.go` (Go structs → generated
`pkg/sextantproto/schemas/*.json`), with `Lifecycle` conflating intent and
observation. Field types below are illustrative, not final.

```
AgentDefinition {

  # ── metadata (identity; set at create, ~immutable) ───────────────
  uuid                UUID
  name                string
  type                string
  template            string
  created_at          timestamp
  updated_at          timestamp

  # ── spec: DESIRED state ──────────────────────────────────────────
  #    operator-written; reconciler read-only.  "What I want."
  spec {
    desired           enum  run | paused | archived      # the intent
    image             string                             # sidecar image ref
    mounts            []MountSpec                         # functional: workspace, gitconfig, ssh, git-dir
    env               map<string,string>
    runtime           { model, permissions, session_id }
    tools             [...]
    resource_limits   { cpu, memory }
    host_pin          string?
    restart_policy    enum  Always | OnFailure | Never    # default OnFailure (§5.3)
    grace_seconds     int                                 # SIGTERM→SIGKILL, default 30 (§8)
    generation        int                                 # ++ on every spec edit (§5.6)
    reactuation_nonce int                                 # ++ by `restart` to force a fresh incarnation (§5 lead)
  }

  # ── status: OBSERVED state ───────────────────────────────────────
  #    reconciler-written (SOLE writer); operator read-only.  "What is true."
  status {
    observed              enum  pending | running | crashed | lost | ended
    phase                 string                          # human-facing rollup
    current_incarnation_id ID
    observed_generation   int                             # last spec generation applied (== generation ⇒ caught up)
    observed_nonce        int                             # last reactuation_nonce applied
    spec_fingerprint      hash                            # what was actually built — drift check (§5.6)
    wire_epoch            int                             # running sidecar's epoch — skew check (§5.8)
    restart_count         int                             # monotonic lifetime (operator visibility, §8)
    crash_window          { count, since }                # windowed budget: 5/10min → terminal (§8)
    last_exit             { code, reason, at }
    session_snapshot      path?                           # durable JSONL snapshot-on-stop (§5.10)
    last_heartbeat_at     timestamp
    last_reconciled_at    timestamp
  }
}
```

**The reconciler's whole job, in these terms:** drive `status` toward
`spec`. Restart if `spec.desired=run ∧ status.observed ∈ {lost,crashed}`;
re-actuate if `status.spec_fingerprint ≠ hash(spec)` **or**
`status.observed_nonce < spec.reactuation_nonce` **or** `status.wire_epoch <
current`; stop if `spec.desired=paused`; tear down if
`spec.desired=archived`. Every enforcer behavior in this RFC is one clause
over this single record.

**Migration from today's `agent.go`:** `Lifecycle` splits into
`spec.desired` (run/paused/archived) + `status.observed`
(running/crashed/lost/ended); `CurrentIncarnationID` moves under `status`;
`Version` stays distinct from the new `spec.generation`;
image/mounts/env/runtime/tools/limits move under `spec`; everything else in
`status` is new.

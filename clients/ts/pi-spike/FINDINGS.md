# TASK-176 spike findings — pi as a first-class sextant bus client

**Verdict: GO for TASK-177**, with the design adjustments in the last section.

This spike validated, against a **real Go bus** and a **real `pi --mode rpc`
process** driving a **real (cheap) Anthropic model**, that a pi session can host
a first-class sextant bus client. The mechanism works end to end: an inbound bus
frame wakes an idle pi agent, the SDK client survives a session transition, a
busy topic is absorbed without wedging, and pi's action stream is bridgeable
onto a bus activity topic. No blocking gap was found.

The deliverable is three files plus this write-up:
- `extension.ts` — the minimal spike extension (the thing TASK-177 grows into).
- `spike.ts` — the AFK driver: stands up the bus, mints scoped creds, launches
  pi in RPC mode with the extension, and makes the five assertions.
- `busharness.ts` — a trimmed copy of the SDK's real-bus harness (its `test/`
  harness is not an exported module).

Run it yourself: `cd clients/ts/pi-spike && npm install && npm run spike`
(needs the Go toolchain on PATH and `ANTHROPIC_API_KEY`; costs a few cents).

## Environment validated

- pi: `@earendil-works/pi-coding-agent` **0.79.8** (the spec named 0.78.1; that
  is no longer the installed version — see "Version pinning" below).
- The wake primitive, RPC mode, the `session_start`/`session_shutdown` reasons,
  and the `tool_execution_*`/`turn_*` observability events are all present in
  0.79.8 exactly as the spec described.
- TS SDK `@sextant/sdk` (epoch 1) builds clean and its own real-bus integration
  test passes (TS↔Go round-trip, scoped-creds identity).

---

## AC#1 — Headless wake: CONFIRMED

**An inbound bus frame wakes an idle pi agent, end to end, in RPC/extension mode.**

The driver boots pi in RPC mode with **no initial prompt**, so the agent sits
idle (confirmed via `get_state` → `isStreaming:false`). A *separate* SDK client
(the "peer", its own scoped creds) publishes a `chat.message` frame to the pi
agent's inbox (`msg.client.<piAgentId>`). The extension's inbox subscription
fires and calls:

```ts
pi.sendMessage(
  { customType: "sextant-bus", content: `Bus message on ${subject} from ${author}:\n${body}`, display: true },
  { triggerTurn: true },
);
```

The driver then observes a **new** `turn_start` → `turn_end` (no `prompt` command
was ever sent over RPC), and the assistant replied
`"Acknowledged: WAKE-1 message received."`. Trace excerpt:

```
inbound      subject=msg.client.<id>  from=<peer>
wake_deliver from=<peer> frameId=<ulid>
turn_start   turnIndex=0
turn_end     turnIndex=0
```

This is the shipped `file-trigger` example's pattern with a real bus subscription
as the trigger instead of a watched file — and it works headless. **PASS.**

---

## AC#2 — Connection survival across session transitions: CONFIRMED (cleared issue 3021), with a pi quirk to handle

**The SDK client is opened at `session_start`, drained+closed at
`session_shutdown`, and the agent is wakeable again after a transition.**

The driver drives a `new_session` (tears the extension runtime down and back up:
`session_shutdown` reason `"new"` → `session_start` reason `"new"`), then has the
peer publish a post-transition frame and asserts a *new* turn fires. It does:
post-transition the extension cleanly closed the old client and re-opened a fresh
one, and a post-transition frame woke a turn — so the live `pi.*` bindings reach
the live session, not a disposed one.

**issue 3021 (`pi.* calls target disposed session after ctx.newSession()`):
NOT reproduced** with this extension's shape, and here is *why* it doesn't apply.
3021 is specifically about an extension **command handler** that calls
`ctx.newSession()` and then keeps calling `pi.sendUserMessage()` on the runtime
it closed over at load time. This extension never calls `newSession()` itself and
never caches `pi`-method references across a transition — it re-runs its whole
setup inside each `session_start` handler and re-opens the SDK client fresh; the
wake path resolves `pi.sendMessage` at call time inside the live handler. The
disposed-binding trap is structurally avoided.

**Spike finding (design-relevant): pi fires `session_start` (reason `"new"`)
TWICE for a single `new_session` in RPC mode.** Reproduced in isolation with a
trivial 6-line extension and *no* bus or LLM:

```
[trivial] session_start reason=startup
[trivial] session_shutdown reason=new
[trivial] session_start reason=new      <-- fires
[trivial] session_start reason=new      <-- fires again
```

A naive `session_start` handler that opens a connection would **leak** the first
one on the second fire — and the second fire briefly tears down the client the
first one just subscribed (a window where a publish can be missed). The spike
hardened the extension with an **idempotency guard**: close any client already
held before opening a new one (observed firing once per transition, collapsing
the double-start so no connection leaks). TASK-177 must carry this forward —
`session_start` is NOT guaranteed once per logical session. **PASS** (the
survival property holds and 3021 is cleared), with the idempotency requirement
and the double-start window noted.

---

## AC#3 — Back-pressure on a busy topic: CHARACTERISED + policy proposed

**A bounded inbound buffer with a drop-oldest policy absorbs a flood without
wedging the agent.**

The driver floods the watched topic with 40 frames in a tight burst while the
agent is mid-turn. The extension's policy:

- if the agent is **idle**, deliver immediately (wake);
- if **busy**, push onto a bounded queue (`MAX_BUFFERED = 16`); when full, **drop
  the oldest** queued frame (the freshest signal wins);
- on each `turn_end`, flush one buffered frame (which re-triggers a turn, which
  on its next `turn_end` flushes the next — the queue drains in order, one wake
  per turn, never stacking unbounded turns).

Observed: the queue filled to 16, then logged 23 drop-oldest events for the
remainder; the agent kept turning and **returned to idle after the burst**
(`isStreaming:false`) — no wedge, no unbounded turn stack, no memory growth.

**Proposed policy for TASK-177:** a bounded per-source queue with drop-oldest is
the right default, because **the durable record lives on the bus** — a wake is
"come look", not at-least-once delivery; the agent can `read`/`fetch` the topic
to recover anything dropped. Refinements to weigh: coalescing a burst from one
author into a single "N new messages on <topic>" wake; a reserved slot for DMs
(inbox) so a topic flood can't starve direct address; and making `MAX_BUFFERED`
and the coalescing window config. **PASS (characterised; policy proposed).**

---

## AC#4 — Agent-action observability: CONFIRMED

**pi's RPC event stream (tool calls, thinking, turn events) is consumable AND
bridgeable to a bus activity topic.**

Two layers, both proven:

1. **RPC stream (the floor):** the driver consumes `turn_start`/`turn_end` and
   `tool_execution_start`/`tool_execution_end` directly off pi's stdout JSONL.
   When driven to run a `bash` tool it saw `tool_execution_start`/`_end` carrying
   the tool name, args, and result.
2. **Bus bridge (the operator path):** the extension subscribes to its own
   `turn_*` and `tool_execution_*` events via `pi.on(...)` and republishes each
   as a `pi.activity` record on a bus topic. A second SDK client (the peer)
   subscribed to that topic and **read back all four kinds**:
   `["turn_start","tool_start","tool_end","turn_end"]`. So a dash client reading
   that topic sees a headless worker's turns AND tool calls without attaching to
   its terminal — the TASK-150/151 "monitor agents from sextant" thread.

(pi also persists every session as JSONL under `~/.pi/agent/sessions/`, an even
lower floor if the live stream is unavailable.) **PASS.**

---

## AC#5 — Security / trust posture: decision for TASK-177

**The agent acts on its OWN scoped credentials, never the operator's.** The
extension reads `SEXTANT_PI_CREDS` (the pi agent's `.creds`) and opens the SDK
`Client` with them. The TS SDK integration test already proves a TS client is a
*distinct identity* in the clients registry, never the operator; the spike
confirmed the running pi agent's bus id equals its minted-agent id, and the peer
is a separate identity. Bus authorship is unforgeable; the pi worker is a
co-equal crew member, not an operator impersonator. **Hard requirement for
TASK-177, satisfied by construction** (own creds, passed in, never the
operator's ambient context).

**Bus-delivered instructions vs pi's permission gates.** The key safety fact: a
bus frame enters pi as **ordinary input** — a `customType:"sextant-bus"` custom
message that triggers a turn. It is *not* privileged. Everything the model then
does flows through pi's normal tool path, so **pi's existing `tool_call`
permission gates apply unchanged.** An extension can register a `tool_call`
handler returning `{ block, reason }`; pi's shipped `permission-gate` example
blocks dangerous `bash` and **blocks-by-default when there is no UI**
(`!ctx.hasUI`) — exactly the headless case. A bus message saying "rm -rf /" does
not get a bypass; it is gated like any other prompt.

pi's own security model (its docs/security.md) is explicit and load-bearing
context: pi is **not a sandbox** — it runs with the user's permissions;
project-trust only guards *input loading* (settings/extensions/skills), not what
tools can do; and prompt injection from untrusted content is "expected
local-agent risk." The consequence for TASK-177:

> **Bus-delivered content is untrusted input and a prompt-injection surface.** A
> bus message can try to make the pi agent do anything a typed prompt could. The
> defenses are the standard ones and belong in the design:
> 1. **Own scoped creds + least privilege** — the worker's bus identity grants
>    only what it needs; a compromised pi worker can't act as the operator or
>    reach topics it wasn't granted.
> 2. **A `tool_call` gate** that is block-by-default for destructive actions in
>    headless mode (pi gives us the hook; ship a sane default, overridable).
> 3. **Run untrusted/unattended pi in a container/VM** per pi's own guidance —
>    the OS boundary is the real isolation, not anything in-process.
> 4. Optionally **trust-tier the wake**: the bus stamps frame author, so the
>    extension can render the author's trust level into the injected message
>    (principal / verified peer / unknown), mirroring the `sextant:startup`
>    skill's trust-stamped-message pattern, so the agent and gate can weigh
>    instructions by source.

**Posture written up; no blocker.** "Own scoped creds" is met; the
prompt-injection surface is real and handled by the layered defenses above, which
TASK-177 should adopt as design, not afterthought.

---

## Go / no-go for TASK-177: **GO**

Every mechanism the design rests on is real and works against a real bus and a
real pi process. Adopt these adjustments into the TASK-177 design:

1. **`session_start` is not once-per-session.** pi fires it twice for a single
   `new_session` in RPC mode. The production extension's open-client path **must
   be idempotent** — close any held client before opening, exactly as the spike's
   guard does. Carry the micro-repro into a regression note.
2. **Back-pressure: bounded queue, drop-oldest, durable record on the bus.**
   Default `MAX_BUFFERED` small and configurable; consider burst-coalescing into
   a single "N new on <topic>" wake and a reserved DM slot so a topic flood can't
   starve direct address.
3. **The observability bridge is a first-class feature, not a debug aid.** The
   `pi.activity` topic worked; shape its record as a small lexicon (kind, turn
   index, tool name/args/result, author = the worker) so the dash can render a
   headless worker like any crew member (ties to TASK-150/151).
4. **Security is layered and explicit:** own scoped creds (done), a
   block-by-default `tool_call` gate for headless destructive actions,
   container/VM for untrusted work, and (recommended) trust-tiering the injected
   wake by frame author. Bus content is an untrusted prompt-injection surface —
   say so in the ADR.
5. **Version pinning.** Pin pi to an exact version in the production package (the
   spike ran on **0.79.8**, not the spec's 0.78.1). The wake/RPC/observability
   surface is stable across that drift, but pin a tested version and re-validate
   this spike's assertions on a bump — the spike driver is the regression harness
   for that.

### Honest caveats
- The spike used a single-host loopback bus and a cheap model; it did not test
  network partitions, a real reconnect storm, or a multi-day unattended run. The
  SDK carries reconnect-resume (ADR-0027) and heartbeat presence, but the
  pi-extension-over-long-uptime path is unproven beyond this spike's minutes.
- The `reload` and `fork` `session_start` reasons were not each driven
  individually; `new` was driven and exercises the same shutdown→start runtime
  teardown/rebuild path the others use. TASK-177 should add a per-reason check if
  reload/fork prove to differ.
- The managed close-and-resume handoff (the PRD's secondary path) was not built
  here — the SDK's cooperative-drain (`drained()` → `close()`) is wired but the
  spike validated bus-addressing (the primary, required path), not the
  drain-handoff dance.

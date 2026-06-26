# @sextant/pi-bus — a pi session as a first-class sextant bus client

A [pi](https://github.com/earendil-works/pi-coding-agent) extension that makes a
pi coding-agent session a **first-class sextant bus client**
([ADR-0043](../../../docs/adr/0043-the-pi-harness-is-a-first-class-bus-client.md),
TASK-177): its own scoped identity, addressable by anyone, woken by an inbound
frame, observable in the dash, and able to publish / read / share artifacts /
move a goal. It is built over the co-equal TypeScript SDK
([`@sextant/sdk`](../sdk), [ADR-0041](../../../docs/adr/0041-clients-are-co-equal-across-languages.md))
and grows the validated TASK-176 spike (its design record is [`FINDINGS.md`](FINDINGS.md)).

It adds **no bus operation**: the wake, the activity stream, the tools, and
`/set-goal` are all ordinary bus traffic any client could issue. The bus learns
nothing about pi; pi gains a membership.

## Use it

Drop the built extension into a pi session and give it the bus wiring in the
environment:

```sh
npm run build:deps && npm run build      # build @sextant/sdk + @sextant/conv-goals + this
SEXTANT_PI_CREDS=/path/to/agent.creds \
SEXTANT_BUS_URL=nats://127.0.0.1:4222 \
  pi --mode rpc -e .../clients/pi-bus/dist/src/index.js
```

The one **required** value is `SEXTANT_PI_CREDS` — the agent's own scoped
credential (mint one with `sextant clients register <name> --kind agent`). It
acts on that identity, never the operator's.

### Configuration (environment, all overridable)

| Var | Default | What |
|---|---|---|
| `SEXTANT_PI_CREDS` | *(required)* | the agent's own scoped `.creds` |
| `SEXTANT_BUS_URL` | — | bus NATS URL (wins over the discovery file) |
| `SEXTANT_BUS_JSON` | — | a `bus.json` discovery file (fallback) |
| `SEXTANT_WATCH_TOPICS` | — | extra topics to follow + wake on (comma/space list) |
| `SEXTANT_ACTIVITY_TOPIC` | `pi.activity.<id>` | the topic the activity bridge publishes to |
| `SEXTANT_GOAL_ID` | — | the default goal `/set-goal` moves when no id is given |
| `SEXTANT_PI_MAX_BUFFERED` | `16` | inbound back-pressure queue bound |
| `SEXTANT_PI_COALESCE_MS` | `1500` | burst-coalescing window (0 disables) |
| `SEXTANT_PI_PREVIEW_MAX` | `600` | activity-bridge arg/result/text preview cap |
| `SEXTANT_PI_GATE_HEADLESS` | `on` | the headless destructive-tool gate (`off` disables) |
| `SEXTANT_PI_HANDOFF_TOPIC` | `pi.handoff` | topic the managed handoff announces relinquished/acquired on |

## What it does (the five spike-mandated adjustments)

1. **Idempotent `session_start`** ([`src/bus.ts`](src/bus.ts)). pi fires
   `session_start` twice for one `new_session` in RPC mode; the open path is
   close-before-open and self-serialising, so no client leaks and no frame is
   missed in the double-fire window.
2. **Bounded back-pressure** ([`src/wake.ts`](src/wake.ts)). A wake is "come look
   at the bus", not at-least-once delivery — the durable record lives on the bus.
   The inbound queue is bounded, drop-oldest, with a **reserved slot for direct
   address** (a topic flood can't starve a DM) and **same-author/same-topic
   coalescing**; it drains one per `turn_end`, so turns never stack.
3. **A first-class `pi.activity` bridge** ([`src/activity.ts`](src/activity.ts),
   [`protocol/lexicons/pi.activity.json`](../../../protocol/lexicons/pi.activity.json)).
   Turns, thinking, the reply, and tool calls are published as `pi.activity`
   records the dash's conversation viewer renders live.
4. **Layered security** ([`src/gate.ts`](src/gate.ts), [`src/trust.ts`](src/trust.ts)).
   Bus content is an **untrusted prompt-injection surface**. Defenses: own scoped
   creds (least privilege); a **block-by-default destructive-tool gate when
   headless** (`!ctx.hasUI`), overridable; the OS boundary (container/VM) for
   untrusted/unattended runs; and **author trust-tiering** of the injected wake
   (principal / peer / unknown). See ADR-0043.
5. **pi pinned to `0.79.8`** ([`package.json`](package.json)); the driven harness
   re-validates the spike's facts on a bump.

## The agent's surface

- **Tools** ([`src/tools.ts`](src/tools.ts)): `sextant_publish`, `sextant_reply`,
  `sextant_read`, `sextant_subscribe`, `sextant_unsubscribe`,
  `sextant_artifact_get` / `_put` / `_list`.
- **Command** ([`src/goal_command.ts`](src/goal_command.ts)): `/set-goal [goalId]
  <criterionId> <status> [headline]`, which moves a goal criterion **through the
  goals convention** ([`@sextant/conv-goals`](../conventions/goals)) — the same
  `goal.<id>` artifact + `goal.update` stream the dash reads.
- **Skill** ([`skill/SKILL.md`](skill/SKILL.md)): teaches the agent the bus
  conventions (verb selection, record shapes, the trust tiers).

## Headless under the dispatcher + the managed handoff (TASK-178)

The primary way to run a pi worker is headless under the reference dispatcher
([`clients/dispatcher`](../../dispatcher)): point `sextant-dispatch
--harness` at the **pi recipe** ([`recipes/pi.sh`](../../dispatcher/recipes/pi.sh),
the sibling of `agent.sh`). The dispatcher mints the worker's own scoped creds
(mint-on-behalf, [ADR-0033](../../../docs/adr/0033-a-dispatcher-mints-its-own-workers.md))
and the recipe launches `pi --mode rpc` with this extension; the worker boots idle
and is woken over the bus — a crew member that happens to be a pi session.

A **managed close-and-resume handoff** ([`src/handoff.ts`](src/handoff.ts),
[`protocol/lexicons/pi.handoff.json`](../../../protocol/lexicons/pi.handoff.json))
releases a worker without two processes fighting one session — **single-owner-at-a-
time**, coordinated over the bus:

1. An owner sends `pi.handoff{verb:"drain"}` to the worker's inbox (a principal or
   verified peer — an unknown client's drain is refused).
2. The worker winds down **cooperatively**: it stops taking new wakes, lets the
   current turn finish, announces `pi.handoff{verb:"relinquished", session:<id>}`,
   drains+closes its bus client (presence → offline — the visible release), then
   exits. pi has persisted the session as JSONL.
3. The operator resumes the JSONL by hand; the dispatcher re-spawns the recipe on
   the **same** session id (`pi --session-id` resumes), and the extension announces
   `pi.handoff{verb:"acquired"}`.

The relinquish completes (worker offline) before any re-spawn acquires the session,
so the two never overlap. The end-to-end handoff (drain → relinquish + exit → resume
→ acquired, with a memory probe that proves the session resumed) is the
`driven:handoff` harness below.

## Tests

```sh
npm test        # unit: the wake/back-pressure policy, the headless gate,
                # the pi.activity bridge, the /set-goal arg parse (no bus, no model)
npm run driven  # the operator-verified AC#5 run: a real pi + a real model on a
                # throwaway HERMETIC bus — DMs the agent, asserts it wakes + replies,
                # its activity streams, and /set-goal moves a dash-visible goal.
                # Needs the Go toolchain + ANTHROPIC_API_KEY; costs a few cents.
npm run driven:handoff  # the TASK-178 run: launches the REAL dispatcher recipe
                # (recipes/pi.sh) as the dispatcher would, then drives the operator
                # path end-to-end — a DMed TASK → an artifact + reply; then the
                # managed handoff (drain → relinquish + exit → re-spawn resume →
                # acquired) with a memory probe proving single-owner resume.
                # Same prerequisites as `driven`.
```

The unit tests run in CI (the `clients-ts` job); the driven run is out of band
(it needs a real model + API credits). The driven harness is the regression
harness for the five adjustments.

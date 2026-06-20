# @sextant/pi-spike — TASK-176 spike

> **This is a SPIKE, not the product.** It validates that
> [pi](https://github.com/earendil-works/pi) can host a first-class sextant bus
> client before TASK-177 commits to the `@sextant/pi-bus` design. Read
> `FINDINGS.md` for the go/no-go and the design adjustments. Do not depend on
> this package.

## What it does

A minimal pi extension (`extension.ts`) that makes a headless pi session a bus
participant:

- opens a TS SDK `Client` on the agent's **own scoped creds** at `session_start`,
  subscribes to its inbox + an optional topic;
- on an inbound bus frame, **wakes the idle agent** via
  `pi.sendMessage({ customType: "sextant-bus", … }, { triggerTurn: true })`;
- bridges pi's `turn_*` / `tool_execution_*` events onto a bus **activity topic**;
- bounded back-pressure (drop-oldest) so a busy topic never wedges the agent;
- drains + closes the client at `session_shutdown`.

`spike.ts` is the AFK driver: it stands up a real Go bus, mints scoped creds,
launches `pi --mode rpc -e extension.js`, and asserts the five spike findings
(headless wake, connection survival, back-pressure, observability, security).

## Run it

```bash
cd clients/ts/pi-spike
npm install          # symlinks ../sdk and pulls pi 0.79.8
npm run spike        # builds the SDK + extension, then runs the AFK driver
```

`npm run spike` chains `build:sdk` (the `../sdk` dep is a symlink, so building it
there reflects through) → `build` → the driver.

Requirements: the Go toolchain on `PATH` (the driver builds + runs the real
`sextant` bus), and `ANTHROPIC_API_KEY` (the driver drives a real cheap model —
a few cents). Override the model with `SEXTANT_PI_MODEL`.

## The extension's env contract

| var | meaning |
|-----|---------|
| `SEXTANT_PI_CREDS` | path to the agent's own `.creds` (required) |
| `SEXTANT_BUS_URL` | bus NATS URL (or `SEXTANT_BUS_JSON`) |
| `SEXTANT_BUS_JSON` | bus.json discovery file (fallback) |
| `SEXTANT_WATCH_TOPIC` | optional topic to subscribe to besides the inbox |
| `SEXTANT_ACTIVITY_TOPIC` | optional topic to publish the agent's activity to |
| `SEXTANT_SPIKE_LOG` | optional JSONL trace path (the driver reads it for assertions) |

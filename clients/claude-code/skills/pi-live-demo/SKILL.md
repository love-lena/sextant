---
name: pi-live-demo
description: Run the capstone one-command live demo that proves a pi agent is a first-class crew member on a sextant bus — a co-equal TypeScript pi client, on its own scoped identity, wakes on a DM, replies over the bus, moves a goal that renders, and streams its thinking + tool-calls to an activity topic the dash renders live. Fully hermetic (a throwaway bus; the operator's real bus + active context are untouched) and self-validating (prints PASS/FAIL per step). Use when the operator asks to run the pi live demo, see a pi agent on the bus end to end, or validate the co-equal-clients payoff hands-on.
---

# pi-live-demo — a pi agent on the operator's bus, end to end

This is the **capstone** demo for the co-equal-clients refactor (TASK-184). It
closes the loop on the whole payoff in one run: a headless **pi coding-agent**,
running as a **co-equal TypeScript bus client on its OWN scoped identity**, wakes
when DM'd, replies over the bus, moves a goal that renders, and streams its
thinking + tool-calls to a bus activity topic the **dash renders live** — so the
operator watches and DMs it like any crew member.

It is **fully hermetic**: it stands up a THROWAWAY bus with `SEXTANT_HOME` pinned
to a temp store on every process, so it **never touches the operator's real bus or
active context**, and tears everything down at the end. It runs a **real Anthropic
model** (a few cents) and is **self-validating** — it prints PASS/FAIL per step and
a final N/N summary.

## How you (the agent) run it

You drive the script underneath; **never hand the operator a raw shell one-liner.**

### Preconditions — check first, fail loud if missing

1. **You are in a sextant repo checkout.** The demo builds the Go bus + dash UI and
   the TS workspace from source, so it needs the repo. Find the repo root (the dir
   with `go.mod` + `clients/ts/pi`). If you are not in a checkout, say so and stop.
2. **`ANTHROPIC_API_KEY` is set** in the environment — the pi agent runs a real
   model. If it is unset, tell the operator to export it and stop (do not invent a
   key, do not run without it).
3. **`pi`, `go`, and `npm` are on PATH** (`pi` lives under `~/.npm-global/bin`; the
   script adds it). If `pi` is missing, tell the operator to `npm i -g
   @earendil-works/pi-coding-agent` and stop.

### Run the demo

Run the bundled script from the repo root:

```
bash docs/demos/pi-live-demo.sh
```

It runs unattended: it installs + builds the TS workspace from clean, builds the Go
bus + dash UI from source, boots the hermetic bus, mints a distinct pi-agent
identity, starts a real `pi --mode rpc`, drives the full operator path, and asserts
each bus-side step. **Read its output**, then relay the PASS/FAIL summary to the
operator plainly. Each step is one of:

- **AC#1 distinct pi identity** — a throwaway bus + a pi agent minted as its OWN
  scoped identity (never the operator's creds).
- **AC#2 wake + reply** — a DM wakes the idle pi agent and it replies over the bus
  as itself.
- **AC#2 goal renders** — `/set-goal` moves a real goal criterion through the goals
  convention (a `goal.update` on `msg.topic.goals`) — the same artifact + stream the
  dash Goals view reads.
- **AC#3 activity streams** — the agent's turns + tool-calls (+ thinking) stream to
  `msg.topic.pi.activity.<id>`, which the dash's conversation viewer renders live.
- **AC#4 dash serving** — the `sextant-dash` web dash is live; the script prints the URL.

### After it passes — the operator watches it live

Once every step is green, the script **keeps the bus, the pi agent, and the dash
alive** and prints the **dash URL**. Relay that URL to the operator and tell them:

- Open the dash; the pi worker (`pi-live-demo-agent`) is **online** in the Agents
  view — a headless crew member.
- Open a **DM** to it and send a message — watch it **wake + reply** on the bus.
- Its **thinking + tool-calls** stream live into its activity conversation.
- The **Goals** view shows the criterion the agent set now **met**.

The script holds the dash up for a watch window (default ~10 min;
`SEXTANT_DEMO_WATCH_MS` overrides — `0` skips the watch phase for a pure AFK
self-test) and tears everything down on Ctrl-C. When the operator is done, stop it
(Ctrl-C) and confirm the teardown ran.

## Reporting

Report the result plainly:

```
pi-live-demo — TASK-184
  AC#1 distinct pi identity ... PASS  (pi agent <id>, own scoped creds)
  AC#2 wake + reply .......... PASS  (woke on DM, replied as itself, tier=principal)
  AC#2 goal renders .......... PASS  (criterion "observable" → met; goal.update announced)
  AC#3 activity streams ...... PASS  (turns + tool calls + thinking on pi.activity.<id>)
  AC#4 dash serving .......... PASS  (dash live at http://127.0.0.1:<port>)
  → N/N PASS — open the dash at <url> to watch + DM the pi worker
```

Do not soften a FAIL — name it, show the evidence the script printed, and stop the
watch phase (a failing step blocks it). State the overall verdict plainly.

## Boundaries

- **Hermetic by construction.** Throwaway bus, `SEXTANT_HOME` pinned to a temp
  store on every process, loopback-only, torn down. It does **not** touch the
  operator's real bus, home, or active context. After a run, `sextant context list`
  still shows the operator's real context (`lena`) active.
- **Real model, real cost.** The pi agent runs a real Anthropic model — a few cents
  per run. It needs `ANTHROPIC_API_KEY`; it never runs without one.
- **From clean.** It builds everything from source (no pre-built binary, no
  `SEXTANT_REPO_ROOT` / `SEXTANT_BIN` overrides). First run is slower (the Go build).
- **It does not write to the operator's bus or merge anything.** The only durable
  side effect is the temp build artifacts under the package's `node_modules`/`dist`
  and the throwaway store, which is removed on teardown.

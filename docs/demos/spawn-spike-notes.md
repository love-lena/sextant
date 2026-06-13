# M5.1 spawn-spike — design notes (TASK-70)

_Spike PoC by canopus, 2026-06-12. Validated live against a **throwaway bus**
(fresh store + port, never the operator's live bus). Research: artifact
`m5-spawn-spike-research`. Feeds M5.2 (the real dispatcher + mint-on-behalf)._

## What the spike proves
A dispatcher can launch a fresh agent that **joins the bus under its own
identity** and runs a task — for two harnesses, via two identity paths — with no
core protocol changes. The join seam is the **`sextant-mcp` stdio server**: any
MCP harness that launches it (handed a store + `enroll.creds`, or explicit creds)
becomes a bus client.

| AC | Mechanism | Status | Evidence (throwaway bus) |
|----|-----------|--------|--------------------------|
| #1 | `claude -p` auto-mint, **keyed** identity | ✅ proven | spawned agent joined as `claude-<session-uuid>` (resume-stable), published hello on `msg.topic.demo`; id captured from the frame author |
| #2 | `codex exec` auto-mint, per-process identity | ✅ proven | codex (gpt-5.5) called `sextant/message_publish`, joined as `claude-90e72272…`, published hello |
| #4 | **Nickname / dispatcher-known id** (hand mint-on-behalf) | ✅ proven | pre-registered `vega`; spawned claude with `SEXTANT_CREDS=vega.creds` joined **as vega** (no new `claude-<hex>`), published under vega's known id |
| #3 | **Wake loop** (one-shot → wake on DM) | ✅ proven | `cmd/spawn-poc` (supervisor, its own SDK client) watched the agent's DM and re-invoked `claude -p --resume`; the woken agent rejoined **under its same keyed id** and published `awake-ack`. Mechanism slice proven token-free; live slice proven against the AC#1 agent |
| #5 | These design notes | ✅ (this doc) | — |

## Launch recipes (verified)
### claude -p (Harness A)
```
claude -p "<task/primer>" \
  --bare --strict-mcp-config \
  --mcp-config '{"mcpServers":{"sextant":{"command":"<sextant-mcp>","env":{"SEXTANT_STORE":"<throwaway-store>"}}}}' \
  --permission-mode bypassPermissions --output-format json < /dev/null
```
- **Isolation:** `--bare` (no plugins/hooks/settings) + `--strict-mcp-config` (ONLY
  the configured MCP server) keep it off the operator's live bus — load-bearing.
- `SEXTANT_STORE` MUST be pinned in the MCP env: `sextant-mcp`'s `defaultStore()`
  otherwise falls back to the user-config dir (the live bus). Pin it.
- `< /dev/null` skips a ~3s "no stdin data" wait.
- `--output-format json` gives a `session_id` (for `--resume`) + cost/usage.
- Identity: `claude -p` sets `CLAUDE_CODE_SESSION_ID`, so auto-mint yields a
  **keyed, resume-stable** handle (`claude-<uuid>`).

### codex exec (Harness B)
```
codex exec "<primer + task>" \
  -c 'mcp_servers.sextant.command="<sextant-mcp>"' \
  -c 'mcp_servers.sextant.env={ SEXTANT_STORE = "<throwaway-store>" }' \
  --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check --cd <dir> < /dev/null
```
- MCP injected via `-c` overrides (no config.toml edit). codex launches the stdio
  server as its own child; creds/store ride in the `env` table.
- No `--system-prompt` flag — fold the primer into the prompt (or an `AGENTS.md`).
- `codex exec resume <session-id>` re-invokes for the wake loop.
- Auth: model auth is codex's own (`codex login` / API key), separate from bus auth.

### Capturing the spawned id
The dispatcher reads the agent's first publish and takes the **frame author** as
the spawned client's id (`sextant read <subj> --since 0`; the author is bus-stamped,
unforgeable) — no need for the agent to self-report. For the nickname path the id
is already known (it's the pre-registered identity).

## Identity seam (the M5.2 input)
- **Path 1 — auto-mint join (works for ANY host today).** Hand the spawned
  `sextant-mcp` the store + `enroll.creds`; it self-enrolls (`selfenroll.EnrollAgent`).
  `claude -p` → keyed/resume-stable; `codex exec` → per-process (not reattachable).
- **Path 2 — nickname + dispatcher-known id (hand mint-on-behalf).**
  `sextant clients register <nick> --kind agent --out <creds>` mints a held identity
  (prints the id); pass `SEXTANT_CREDS=<creds>` → the agent joins as `<nick>`.
- **Finding — the `claude-` prefix is a misnomer.** Auto-mint names every agent
  `claude-<hex>` regardless of harness (`agentContextName()` hardcodes the prefix) —
  so a codex agent shows up as `claude-90e72272…`. **M5.2's mint-on-behalf should
  assign a correct, harness-aware nickname** (this is the strongest concrete reason
  for the nickname leg of mint-on-behalf, beyond known/stable/scoped).

## Wake loop (AC #3) — proven (`cmd/spawn-poc`)
`claude -p` / `codex exec` are **one-shot**: they run, publish, and go **offline**
(confirmed — the spawned agents showed `offline` in `clients list` after exit), and
they are never retriggered. The **supervisor — its own bus client that implements
the SDK** (lena's refinement; starts simple, grows into M5.2's dispatcher) makes
them wake-on-message with no core change. Built and proven in `cmd/spawn-poc`:
1. `sextant.Connect(ctx, Options{CredsPath:<disp creds>, ConnInfoPath:<store>/bus.json})`.
2. `client.Subscribe(ctx, "msg.client.<agent-id>", handler, sextant.DeliverAll())` —
   the agent's DM is always watched; `--watch <subj>` adds more (see exit hook).
3. handler: on an inbound message (from anyone but the agent/itself), run `--on-wake`
   with the message text in `$SX_WAKE_TEXT` — a `claude -p --resume <session>` /
   `codex exec resume <id>` adapter — so the agent wakes, reads, and acts.
`--once` exits after the first wake; `--deadline` / `--wake-timeout` make a wedged
wait or re-invocation fail loud (never a silent hang). SDK surface used: `Connect`,
`Subscribe(subject, Handler, DeliverAll())`, `DisplayName`/`ID`, `Drained`; the
inbound text is decoded from `Message.Frame.Record` (`chat.message.text`).

**Proven two ways** (throwaway bus): a **mechanism** slice where `--on-wake` just
publishes via the CLI — token-free, deterministic, proves the whole loop — and a
**live** slice that re-invoked the AC#1 agent with `claude -p --resume`: the woken
agent **rejoined under its same keyed bus id** (the `awake-ack` frame's author equals
the AC#1 agent's id — resume-stable identity) and published. The DM sender must be a
**distinct identity** from the dispatcher; the supervisor skips frames it or the agent
authored, so a self-sent wake is (correctly) ignored.

**Finding — MCP readiness on `--resume` is racy.** On resume Claude Code re-launches
the MCP server and may begin the turn while it is still `pending`; an immediate tool
call then hits `No such tool available` and the model can give up. Observed
intermittently (one run `pending` → failed, one `connected` → clean). **Mitigation:**
the wake adapter primes the agent to retry a not-yet-available tool (8×). This is a
harness/adapter concern, not a bus concern — it belongs in the `--on-wake` script.

**Exit hook (lena's refinement, 2026-06-12) — the companion to this loop.** A one-shot
agent that wants to keep listening on a *topic* (not just its DM) has no way to say so
before it ends. So the spawned agent gets a Stop/**exit hook** that asks it "do you
need to do anything (e.g. subscribe to a topic) before ending?"; the agent's answer
becomes additional `--watch` subjects the supervisor then watches and wakes it on.
`cmd/spawn-poc` is the consuming half (the watch set is a flag today); the hook that
*produces* the watch set is an M5.2 client concern. Open: re-invoke dedup/coalescing
of overlapping wakes (the PoC re-invokes per message); an SDK-host variant that owns
the loop in-process; a full interactive session is woken by the plugin for free.

## Inputs for M5.2 (the real dispatcher)
- **mint-on-behalf** (the lone serial core change): buys a dispatcher-**known** id
  up front (for `spawn.ack`/lifecycle), a resume-**stable** id, a **scoped** per-agent
  cred (vs broadcasting `enroll.creds` to every child), and a correct **nickname**
  (the `claude-` prefix finding). Joining itself is NOT gated on it.
- **supervisor/wake-loop** is client-side (parallel module), grows from `cmd/spawn-poc`.
- **exit hook → watch set**: the spawned agent's Stop/exit hook declares the topics it
  wants kept-alive on; the supervisor watches those (its `--watch` set today) and wakes
  it on traffic there, not just on its DM.
- **MCP-readiness on resume**: the wake adapter must tolerate a still-`pending` MCP
  server on the first resumed turn (retry primer here; M5.2 may gate the wake on a
  readiness probe instead).
- Couples to: zombie-client cleanup + liveness-heartbeat (a one-shot exit leaves a
  stale registry entry, shown as `offline`); recursion/fan-out caps; re-invoke dedup.

## Safety note
Every experiment ran on a throwaway bus (`sextant up --store <tmp> --port`), fresh
per run; `--bare --strict-mcp-config` + a pinned `SEXTANT_STORE` kept all spawns off
the operator's live bus. Verified: no spawned identity ever appeared on the live bus.

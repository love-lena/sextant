---
title: prompt_agent should refuse when sidecar heartbeat is stale (deeper safety net beyond lifecycle)
status: open
priority: P2
created_at: 2026-05-26T22:30-07:00
labels: [feature, daemon, rpc, resilience, operator-experience, needs-input]
discovered_in: follow-up from [[bug-prompt-agent-accepts-when-sidecar-gone]] — lifecycle check covers clean exits; heartbeat staleness covers kill-9 / OOM / host-crash cases that bypass the lifecycle publish
---

## Needs Lena's input

Two thresholds and one design choice in this ticket — `heartbeat_staleness_threshold` (default 30s), `heartbeat_startup_grace` (default 60s), and the "no heartbeat yet during startup grace → trust lifecycle" trade-off. The defaults are inherited from the parent ticket but have real operator consequences (a too-aggressive threshold flaps; a too-lax one misses kill-9). Worth a call before implementing.

## Summary

`bug-prompt-agent-accepts-when-sidecar-gone` landed the lifecycle check (option 3 from that ticket's fix-shape): if the recorded lifecycle isn't `running`, prompt_agent refuses with `ErrCodeAgentNotReachable` + remedy. That closes the common case where the sidecar published `transition=ended` or `transition=crashed` on its way out.

Still open: the case where the sidecar dies WITHOUT publishing a lifecycle transition — `kill -9`, OOM killer, host crash, container runtime panic. The lifecycle field stays `running`, prompt_agent accepts, and the prompt vanishes into a subscribed-by-nobody inbox subject.

The original ticket called this out as option (1) "the deeper safety net": cache the last heartbeat timestamp per agent, refuse prompt_agent if the heartbeat is older than ~30s.

## Why P2

The lifecycle watcher catches all sidecar-driven shutdowns (the well-behaved majority). The remaining gap is real but small in practice: agents under normal operation publish heartbeats every few seconds and clean lifecycle transitions on shutdown. The kill-9 path is the edge case operators hit during incident response, where they'd already be inclined to `sextant agents check` (or its eventual existence) rather than `prompt_agent` blindly.

## Fix shape

Model on `pkg/sextantd/lifecycle_watcher.go`:

1. **`pkg/sextantd/heartbeat_cache.go`** — `HeartbeatCache` subscribes core NATS to `agents.*.heartbeat`, maintains `map[uuid.UUID]time.Time` (last-seen, mutex-protected), exposes `LastSeen(uuid.UUID) (time.Time, bool)` for handlers.

2. **Wire into daemon.go** alongside `lifecycleRT` — start after rpcRT, stop before conn close.

3. **Add `HeartbeatLookup` interface to handlers package** so prompt_agent (and future RPCs) can read the cache without depending on the full sextantd package.

4. **Update `prompt.go`** to read the cache AFTER the lifecycle check:
   - If lifecycle = running AND we have a heartbeat AND `time.Since(lastSeen) > 30s` → refuse with `ErrCodeAgentNotReachable` + remedy `sextant agents check <uuid>` (or restart if check confirms dead).
   - If lifecycle = running AND we have NO heartbeat AND lifecycle has been running for > 60s (startup grace) → refuse with the same remedy.
   - Otherwise → publish (existing behavior).

   The "no heartbeat yet" case (freshly-spawned agent in its startup grace window) is intentionally permissive: trust the lifecycle, let prompt_agent through.

5. **Threshold knobs** via config (`heartbeat_staleness_threshold = "30s"`, `heartbeat_startup_grace = "60s"`) with defaults baked in.

## Acceptance

- `TestPromptAgentRejectsStaleHeartbeat` — fake cache reports a 60s-old heartbeat for a running agent; prompt_agent returns ErrCodeAgentNotReachable with the remedy substring.
- `TestPromptAgentAllowsRecentHeartbeat` — fake cache reports a 1s-old heartbeat; prompt_agent accepts.
- `TestPromptAgentAllowsRunningAgentInStartupGrace` — no heartbeat yet, lifecycle.UpdatedAt < grace ago; prompt_agent accepts.
- `TestPromptAgentRejectsRunningAgentBeyondStartupGrace` — no heartbeat ever, lifecycle has been running > 60s; prompt_agent refuses.

## Related

- [[bug-prompt-agent-accepts-when-sidecar-gone]] — the immediate fix, this ticket is the deeper safety net.
- [[bug-agents-list-stale-lifecycle]] — the lifecycle freshness this builds on top of.
- [[bug-sidecar-nats-disconnect-no-reconnect]] — adjacent: the disconnect case is what triggers many heartbeat gaps in practice.
- [[feat-sextant-agents-check]] — the per-agent diagnostic tool's `--ping` variant complements this check.

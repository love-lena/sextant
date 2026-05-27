---
title: sextant doctor --agents — scan every registered agent for lifecycle drift
status: open
priority: P2
created_at: 2026-05-26T15:05-07:00
labels: [feature, cli, doctor, operator-experience, self-serve]
discovered_in: chat TUI Checkpoint C — operator hit a stale-lifecycle agent and the only existing way to find it was per-agent manual investigation
---

## Summary

`sextant doctor` currently checks daemon-level health (daemon liveness, NATS, ClickHouse, config). Add a `--agents` flag that extends the scan to every registered agent: walk the agents list, run the `[[feat-sextant-agents-check]]` probe against each, flag any that fail. Output a one-line-per-agent summary plus a footer that surfaces the worst-case verdict.

```
$ sextant doctor --agents
daemon          ok (pid 98830, uptime 18h)
nats            ok
clickhouse      ok
config          ok

agents          22 registered, 4 running, 18 archived
  worktree-smoke    running    ok
  dev-8             running    stale_record ⚠   → sextant agents restart fab99637-…
  dev-9             running    healthy
  dev-10            running    healthy
  assistant         running    ended ⚠           → sextant agents restart 2b5fcfe4-…

verdict        2 agents need attention.
```

## Why P2

This is the daily-driver health check. Currently the operator has no way to discover that "5 of my running agents are actually dead" without spot-checking each one. Bulk-scanning surfaces the kind of drift that `[[bug-agents-list-stale-lifecycle]]` enables and gets the operator into a remediation loop fast.

## Implementation shape

1. Add `--agents` flag to `cmd/sextant/doctor.go`.
2. Per-agent probe shares logic with `[[feat-sextant-agents-check]]` — refactor that issue's helpers into a reusable package (`pkg/agenthealth/` or inline in `cmd/sextant/agents_health.go`) so both `doctor --agents` and `agents check` call the same code.
3. Skip archived agents by default (`--include-archived` to opt-in — useful for triaging "what happened to dev-12?").
4. Concurrency: probe agents in parallel (errgroup, max N=8). Each probe is a few sub-RPCs + a container check — single-threaded would be slow on large fleets.
5. `--json` flag emits one line per agent.

## Acceptance

- `TestDoctorAgentsFlagsStaleAgent` — synthetic setup: one healthy agent, one with `lifecycle=ended` + missing container; `doctor --agents` exits non-zero, output contains the stale agent + its remedy command.
- `TestDoctorAgentsAllHealthyExits0` — all agents healthy → exit 0.
- `--json` schema is stable per agent: `{name, uuid, verdict, remedy?}`.

## Related

- `[[feat-sextant-agents-check]]` — single-agent counterpart; share implementation.
- `[[bug-agents-list-stale-lifecycle]]` — root-cause; once fixed, the "stale_record" verdict becomes rarer but the tool stays useful for `ended`/`crashed` detection.
- Existing `cmd/sextant/doctor.go` daemon checks — same UX pattern (one-line-per-check).

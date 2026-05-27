---
title: Lifecycle watcher should drop envelopes from stale incarnations (not just yield to archived)
status: open
priority: P3
created_at: 2026-05-27T10:55-07:00
labels: [feature, daemon, observability, lifecycle, needs-input]
discovered_in: Codex adversarial review of the lifecycle watcher CAS fix — the immediate fix yields when the current state is archived, but the full hardening also needs incarnation-ID filtering so envelopes from a now-restarted prior incarnation can't muddy the record
---

## Needs Lena's input

The immediate codex-finding-1 fix (commit `dca779c`) covers the archive-vs-ended race via two mechanisms: JetStream KV CAS (Update with revision) + an archive-guard that yields when the current state is `LifecycleArchived`. That closes the operator-visible bug.

The broader hardening codex recommended — "gate lifecycle updates by the active incarnation" — needs a schema decision before it's implementable:

1. **Where does the active incarnation live?** Three options:
   - Add `CurrentIncarnationID uuid.UUID` to `sextantproto.AgentDefinition`. Cheap, but touches every consumer (shipper, list_agents projection, JSON schema, history rows).
   - Query the `agent_incarnations` KV from the watcher per envelope. No schema change but a second KV round-trip on every lifecycle event.
   - Track the active incarnation in an in-memory map keyed by UUID inside `LifecycleWatcher`. Lost on daemon restart; needs warm-up on boot.
2. **Who updates it?** `spawn_agent` and `restart_agent` know the new incarnation; `kill_agent` and `archive_agent` clear it. Either every handler writes the field directly, or there's a publish-and-subscribe pattern (the watcher itself listens for `transition=started` and notes the new IncarnationID).
3. **What's the failure mode if the watcher gets it wrong?** With CAS + archive-guard, a stale-incarnation envelope that beats the operator's archive still gets dropped. The unfiltered cases left are: stale `paused` after a restart back to `running`, stale `crashed` after a restart that succeeded. Both are race-on-restart edge cases; how often they happen in practice + how bad the operator UX is when they do should inform whether the extra plumbing is worth it.

## Until this lands

The CAS + archive-guard in `pkg/sextantd/lifecycle_watcher.go` covers the most operator-visible race (archive_agent vs. stale ended). The remaining envelope-from-prior-incarnation cases are documented in this ticket but not yet guarded.

## Related

- `[[bug-agents-list-stale-lifecycle]]` — parent ticket the watcher closed.
- `[[feat-prompt-agent-heartbeat-staleness]]` — sibling resilience work, also needs-input.
- `pkg/sextantd/lifecycle_watcher.go` `watcherShouldYield` — the guard this ticket would extend.
- `pkg/rpc/handlers/spawn.go` / `restart.go` / `kill.go` / `archive.go` — the handlers that would write the new incarnation field.

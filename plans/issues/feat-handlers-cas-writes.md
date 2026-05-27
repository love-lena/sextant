---
title: Handlers should use CAS (jetstream.KeyValue.Update with revision) on AgentDefinition writes
status: open
priority: P3
created_at: 2026-05-27T11:25-07:00
labels: [feature, daemon, rpc, lifecycle, follow-up, needs-input]
discovered_in: Codex adversarial-review caught restart_agent's plain-Put race vs concurrent archive/kill; the immediate fix re-reads + refuses, but a full CAS pattern would close the window entirely (and the same shape applies to other handlers)
---

## Needs Lena's input

The immediate fix (restart_agent re-reads + refuses to overwrite a concurrent archive — commit `a7ff5e0`) closes the operator-visible failure mode. The full hardening Codex actually recommended is CAS-on-write via `jetstream.KeyValue.Update(ctx, key, value, revision)`, but the `AgentMutableKV` interface in `pkg/rpc/handlers/spawn.go` doesn't expose Update today. A complete fix requires:

1. **Decide the AgentMutableKV interface shape.** Options:
   - Add `Update(ctx, key, value, revision) (uint64, error)` to `AgentMutableKV`. Every handler test fake grows the method. Real `jetstream.KeyValue` already satisfies it (the lifecycle watcher uses it).
   - Add a separate `AgentCASKV` interface that handlers needing CAS embed. Avoids forcing fakes that don't need CAS to grow methods.
   - Wrap `putJSON` with a `casPutJSON` variant that takes a revision. Migration handler-by-handler.
2. **Decide retry semantics.** On CAS conflict, restart could:
   - Re-read + re-apply (the same shape the watcher uses with a 3-retry budget).
   - Refuse + rollback (what the current mitigation does, but via CAS error instead of a re-read+guard).
   The semantics for restart vs the watcher differ — restart has side effects (a new container spawned) that need cleanup on conflict.
3. **Scope of the migration.** Which other handlers should also adopt CAS? `archive_agent`, `kill_agent`, `restart_agent`, `update_agent` (when it lands) all read-mutate-write the def and are candidates. Doing them all together vs incremental matters for the test surface.

The immediate restart-vs-archive race is closed by the re-read-and-check mitigation; the question is how aggressive to be about hardening the rest of the read-mutate-write surface.

## Related

- `pkg/rpc/handlers/restart.go` — re-read mitigation lives here.
- `pkg/sextantd/lifecycle_watcher.go` — already uses CAS (Update+revision) on `LifecycleDefinitionsKV`; precedent for the pattern.
- Codex adversarial-review round 4 — flagged restart's plain-Put pattern as the no-ship.

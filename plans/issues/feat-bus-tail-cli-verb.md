---
title: Add `sextant tail <subject>` CLI verb for generic bus subscription
status: open
priority: P3
created_at: 2026-05-25T02:09-07:00
labels: [feature, cli, observability]
discovered_in: operator monitoring multi-agent dispatch
---

## Summary

The architecture is "everything on the bus" but the CLI only exposes two narrow tails: `sextant audit tail` (audit.>) and `sextant conversation <agent>` (agents.<uuid>.frames). To watch arbitrary subject patterns, operators have to install the `nats` CLI and pass creds manually. This is a small but real gap in the operator-side observability surface.

## Proposed

```
sextant tail <subject>             # subscribe + print envelopes in human-readable form
sextant tail <subject> --json      # raw JSON for scripting
sextant tail <subject> --from-seq N   # gap-fill from stream sequence
```

Subjects accept NATS wildcards (`*` and `>`). Common cases the verb covers:

- `sextant tail '>'` — full firehose
- `sextant tail 'agents.>'` — every agent's events
- `sextant tail 'agents.*.lifecycle'` — lifecycle across all agents (newly-spawned + dying)
- `sextant tail 'telemetry.>'` — OTel firehose
- `sextant tail 'sextant.system.>'` — daemon self-management events

## Implementation

`cmd/sextant/tail.go` — thin wrapper. Reuses `pkg/client.Subscribe` (already typed as `Message`). Prints `[ts] subject  kind=... summary` per envelope, with `--json` dumping the raw envelope. The Subscribe path already handles auth (operator creds) + reconnection — this is just a CLI surface on top of the library.

## Acceptance

`TestTailGenericSubject`: start sextantd in test mode, run `sextant tail 'audit.>'`, publish a stub `audit.test` envelope, assert the tail process prints a line containing the envelope's id within 5s.

## Related

- `cmd/sextant/conversation.go` — partial precedent for the subscribe + pretty-print pattern
- `cmd/sextant/audit.go` — `audit tail` subverb; this generalizes
- `specs/cli/commands.md` — needs a new top-level entry

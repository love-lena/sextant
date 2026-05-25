---
title: Add `sextant tail <subject>` CLI verb for generic bus subscription
status: resolved
priority: P3
created_at: 2026-05-25T02:09-07:00
resolved_at: 2026-05-25T00:00-07:00
labels: [feature, cli, observability]
discovered_in: operator monitoring multi-agent dispatch
resolved_by: feat-lead-fab99637-001 (sextant dev-8)
---

## Resolution

Landed on branch `feat-lead-fab99637-001`:

- **CLI verb**: `cmd/sextant/tail.go` (`runTail`). Thin wrapper over
  `pkg/client.Subscribe` — same auth and connect path every other
  operator verb uses (`parseCommonOpts` + `connectAgent`). Flags:
  `--from-seq N` (rebind the consumer at a stream sequence so the
  operator can gap-fill after a disconnect) and `--json` (NDJSON
  envelope dump instead of the pretty one-liner).
- **Pretty renderer** (`renderTailEnvelope` + `summarizeEnvelope`):
  `[ts] subject  kind=<kind>  <summary>`, with summaries tailored per
  envelope kind (frame: frame_kind + tool_name; lifecycle: agent +
  transition; audit: actor + action + result; heartbeat: from
  address). Unknown kinds fall back to a truncated payload preview.
- **Dispatch wiring**: `cmd/sextant/main.go` registers the verb and
  the top-level usage block lists it alongside `audit` and `traces`.
- **Spec**: `specs/cli/commands.md` adds the verb under
  "Top-level structure" and gains a §"`sextant tail`" section
  documenting subject patterns, flag semantics, and the
  one-stream-per-consumer constraint that prevents a bare `>`
  firehose subscription from working with a single ordered consumer.
- **Acceptance test**: `TestTailGenericSubject` in
  `cmd/sextantd/tail_test.go` boots the daemon harness, spawns
  `sextant tail 'audit.>' --json` as a subprocess, publishes a stub
  `audit.test` envelope via the operator JetStream client, and
  asserts the tail emits a line whose JSON decodes back to the stub
  envelope's id within 5s.

The verb generalizes the audit-only `sextant audit tail` and the
agent-only `sextant conversation`: any subject in any JetStream stream
is now reachable from the operator CLI without dropping to a separate
`nats` install.

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

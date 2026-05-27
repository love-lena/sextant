---
title: Wire cmd/sextant --json sites + error paths through pkg/cliout envelopes
status: in-progress
priority: P3
created_at: 2026-05-27T00:05-07:00
labels: [feature, cli, output-protocol, follow-up]
discovered_in: feat-cli-output-protocol landed pkg/cliout but the subagent stalled before wiring the cmd/sextant CLI sites

---

## Progress (2026-05-27)

`writeJSON` (cmd/sextant/agents.go) now wraps payloads in
`cliout.Envelope` via `cliout.EnvelopeFromCommand(cmd, v)` — every
`writeJSON(cmd, out, payload)` call now emits `{data: payload, meta:
{version: 1, command: "agents.list"}}`. Call sites swept across
`cmd/sextant/*.go`.

Remaining work, filed as separate follow-ups:

- **Bespoke JSON writers** (agents_check.go `renderAgentCheck`,
  tail.go's `renderTailEnvelope`) still emit raw payload JSON. Move
  them through the envelope or document why they're exempt.
- **Error envelope on `--json`** — error returns from RunE still
  surface as plain text errors. Wrap with `cliout.WriteErrorEnvelope`
  when `globalFlags.asJSON` is set.
- **Exit code 10 for empty results** — `exitNoResults` sentinel,
  thread through `agents list`, `pending list`, `audit query`,
  `events tail`-with-bound, `traces show` when the trace is missing.
- **`specs/cli/commands.md` § "Exit codes"** to document 10.

See [[feat-cli-output-protocol-tail-and-errors]] and [[feat-cli-exit-code-no-results]] for the split.

## Summary

`pkg/cliout/` exists on main (commit `dd32a58`) with the envelope contract:

```go
type Envelope struct { Data any; Meta MetaInfo }
type MetaInfo struct { Version int; Command string }
type ErrorEnvelope struct { Error ErrorInfo }
type ErrorInfo struct { Code string; Message string }
```

Tests pass. What's missing: every `cmd/sextant/*.go` callsite that today writes raw payload JSON under `--json` needs to wrap via `cliout.EnvelopeFromCommand(cmd, data)`, and every error path under `--json` needs to emit the error envelope.

## Fix shape

1. Sweep every site that calls `writeJSON(out, payload)` and rewrite to `cliout.WriteEnvelope(out, cmd, payload)` (or whatever the package's helper is — check `pkg/cliout/envelope.go`).
2. `meta.command` resolves from `cmd.CommandPath()` with spaces → dots (`agents.list`, `events.tail`).
3. Add stable error codes: `AGENT_NOT_FOUND`, `DAEMON_UNREACHABLE`, `INVALID_REF`, `RPC_TIMEOUT`, `USAGE_ERROR`, `NO_RESULTS`.
4. Wrap every non-zero exit path under `--json` so stderr is the error envelope.
5. Add `exitNoResults = 10` to `cmd/sextant/main.go`. Use a sentinel `errNoResults` so `exitCodeFor` branches on it. Thread through `agents list`, `pending list`, `audit query`, `events tail`-with-bound, `traces show` (when the trace doesn't exist).
6. Update `specs/cli/commands.md` § "Exit codes" to document 10.
7. One envelope-shape test per migrated command (verify `{data, meta:{version,command}}` shape).

## Acceptance

- `sextant agents list --json` output matches `{"data":[...], "meta":{"version":1, "command":"agents.list"}}`.
- `sextant agents show 00000000-... --json` writes `{"error":{"code":"AGENT_NOT_FOUND","message":"..."}}` to stderr, exits 1.
- `sextant pending list --json` with no pending requests writes `{"data":[], "meta":...}` and exits 10.
- `specs/cli/commands.md` § "Exit codes" lists 10.

## Related

- `[[feat-cli-output-protocol]]` — parent ticket (pkg/cliout landed there).
- `[[chore-cobra-errcheck-debt]]` — the cobra migration's fmt.Fprintf sweep pairs naturally with this rewrite.
- `conventions/tui-conventions.md` § "Tier 0 → JSON contract".

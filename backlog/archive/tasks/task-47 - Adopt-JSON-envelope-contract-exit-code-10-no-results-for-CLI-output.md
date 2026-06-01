---
id: TASK-47
title: Adopt JSON envelope contract + exit code 10 (no-results) for CLI output
status: Done
assignee: []
created_date: '2026-05-26 20:33'
labels:
  - feature
  - cli
  - output-protocol
  - 'slug:feat-cli-output-protocol'
  - P3
  - 'closed:resolved'
dependencies: []
priority: low
ordinal: 47000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
## Resolution

Shipped across three commits on main:

- `dd32a58` — **`pkg/cliout/`** package with `Envelope`, `MetaInfo`, `ErrorEnvelope`, `ErrorInfo` + stable error code constants. Tests cover the envelope round-trip and ErrorEnvelope shape.
- `e916508` — **`writeJSON` envelope sweep** in `cmd/sextant/`. Every `--json` site that calls `writeJSON(cmd, out, v)` now emits `{data: v, meta: {version: 1, command: <dotted-path>}}`.
- `0ebae51` — **Exit code 10 (no-results)** + agents check envelope. `exitNoResults` sentinel in `cmd/sextant/main.go`; `errNoResults` threaded through `agents list` and `pending list` empty-result paths. `specs/cli/commands.md` § "Exit codes" documents 10. `agents check --json` wraps the verdict in the envelope.

Split off as separate tickets:

- [[feat-cli-exit-code-no-results]] — resolved alongside this.
- [[feat-cli-output-protocol-tail-and-errors]] — needs-input. Wraps the remaining tail.go NDJSON design decision and the error-envelope-under-`--json` execution work.
- [[feat-cli-output-protocol-wiring]] — resolved as the follow-up that landed the wiring work this ticket started.

## Progress (2026-05-26)

`pkg/cliout/` shipped on main (commit `dd32a58`) with the
`Envelope`, `ErrorEnvelope`, and supporting types. Tests in
`pkg/cliout/envelope_test.go` cover the round-trip + error shape.

**Remaining**: wire every `cmd/sextant/*.go` `--json` site to wrap
its payload via `cliout.EnvelopeFromCommand`. Wrap error paths
under `--json` with the error envelope. Add `exitNoResults = 10`
to `cmd/sextant/main.go` and thread the sentinel through commands
that can legitimately return empty (`agents list`, `pending list`,
etc.). Update `specs/cli/commands.md` § "Exit codes".

The subagent that started this work stalled mid-flight after
writing the package but before wiring the CLI. Filed as
[[feat-cli-output-protocol-wiring]] so the next session has a
focused starting point.

## Summary

`conventions/tui-conventions.md` (Tier 0 → JSON contract) pins a
stable output protocol:

```json
{
  "data": [...],
  "meta": {"version": 1, "command": "queue list"}
}
```

Errors get an envelope too, on stderr with non-zero exit:

```json
{"error": {"code": "AGENT_NOT_FOUND", "message": "no agent with id xyz"}}
```

Schema rules: fields can be added, never removed or renamed; types
don't change; enums grow but don't reorder. Breaking changes bump
`meta.version`. Error codes are stable; messages are human and can
change.

The conventions doc also pins **exit code 10 = no results found** as
distinct from real errors (so shell loops can branch on it).

Current state:

- `cmd/sextant/agents.go:625` (`writeJSON`) emits the raw proto
  response with no envelope. Same for every other `--json` path.
- `cmd/sextant/main.go:134` defines exit codes 0 (`exitOK`), 1
  (`exitUser`), 2 (`exitSystem`). No exit code 10.
- `specs/cli/commands.md` § "Exit codes" only documents 0/1/2.

## Fix shape

1. Define a small `pkg/cliout/envelope.go` package:

   ```go
   type Envelope struct {
       Data any      `json:"data"`
       Meta MetaInfo `json:"meta"`
   }

   type MetaInfo struct {
       Version int    `json:"version"`
       Command string `json:"command"`
   }

   type ErrorEnvelope struct {
       Error ErrorInfo `json:"error"`
   }

   type ErrorInfo struct {
       Code    string `json:"code"`    // stable, screaming-snake
       Message string `json:"message"` // human-readable
   }
   ```

2. Wrap every `--json` emission site in `cmd/sextant/` with
   `EnvelopeFromCommand(cmd, data)`. The `command` field should
   resolve to the canonical dotted command path (`agents.list`,
   `pending.list`, etc.).

3. Wrap every error path that exits non-zero with the error
   envelope when `--json` is set. Define stable codes for the common
   shapes: `AGENT_NOT_FOUND`, `DAEMON_UNREACHABLE`, `INVALID_REF`,
   `RPC_TIMEOUT`, `USAGE_ERROR`, `NO_RESULTS`.

4. Add `exitNoResults = 10` to `cmd/sextant/main.go` and thread it
   through commands that can legitimately return zero results
   (`agents list`, `pending list`, `audit query`, `tail`-with-bound,
   `traces show` when the trace doesn't exist). Use a sentinel
   error type (`errNoResults`) so `exitCodeFor` can branch on it.

5. Update `specs/cli/commands.md` § "Exit codes" to document
   exit code 10 alongside 0/1/2.

## Schema evolution rule

A new field on `Envelope.Data` or any payload struct is additive.
Renames, removals, or enum reorderings require bumping
`meta.version` and gating on a CLI flag (`--meta-version=2`) for at
least one release. Codify in `pkg/cliout/doc.go`.

## Acceptance

- `sextant agents list --json` output matches:
  `{"data":[...], "meta":{"version":1, "command":"agents.list"}}`.
- `sextant agents show 00000000-... --json` (nonexistent agent) writes
  `{"error":{"code":"AGENT_NOT_FOUND","message":"..."}}` to stderr,
  exits 1.
- `sextant pending list --json` with no pending requests writes
  `{"data":[], "meta":...}` to stdout, exits 10.
- `specs/cli/commands.md` § "Exit codes" lists 10.
- One test per error code asserts both envelope shape and exit code.

## Open

- Should the envelope ride a top-level `--meta-version` flag, or
  always emit `version: 1` and bump implicitly when we change shape?
  Lean implicit-bump until v2 forces the issue.

## Related

- `conventions/tui-conventions.md` § "Tier 0 → JSON contract"
- `specs/cli/commands.md` § "Output formats" / "Exit codes"
- [[feat-cli-cobra-fang-migration]]
<!-- SECTION:DESCRIPTION:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Migrated from plans/issues/feat-cli-output-protocol.md
Discovered in: CLI/TUI conventions adoption
Original created_at: 2026-05-26T20:33-07:00
Resolved at: 2026-05-27T04:00-07:00
<!-- SECTION:NOTES:END -->

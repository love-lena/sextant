---
title: Output protocol — sweep bespoke JSON writers + wrap error paths under --json
status: open
priority: P3
created_at: 2026-05-27T03:20-07:00
labels: [feature, cli, output-protocol, follow-up]
discovered_in: feat-cli-output-protocol-wiring landed the writeJSON sweep but two corners still emit raw payloads — agents_check.go (renderAgentCheck) and tail.go (renderTailEnvelope) — and the --json error paths still surface as plain text
---

## Progress (2026-05-27)

`agents_check.go::renderAgentCheck` swept — now wraps the AgentCheck
in `cliout.WriteEnvelope`. Test updated to decode through `cliout.Envelope`.

Remaining:

1. **`cmd/sextant/tail.go::renderTailEnvelope`** — emits raw envelope NDJSON. **Special case**: tail's NDJSON contract may justify staying raw (it's not a single response, it's a stream). Decide explicitly: either wrap each line in `cliout.Envelope` (NDJSON of envelopes, `meta.command = "events.tail"` repeated per line) or document why tail stays raw.

2. **Error paths under `--json`** — RunE error returns flow into the cobra default error printer, which writes plain text to stderr. With `globalFlags.asJSON` set the CLI should instead emit `cliout.WriteErrorEnvelope(stderr, code, msg)`. Stable codes already exist in `pkg/cliout/envelope.go` (`CodeAgentNotFound`, `CodeDaemonUnreachable`, etc.). Map each error path.

## Fix shape

For (1): trivial — replace the bespoke marshal with `cliout.WriteEnvelope(w, cliout.EnvelopeFromCommand(cmd, check))`. Update `TestRenderAgentCheckJSONShape` to decode into `cliout.Envelope` first.

For (2): decide on tail's NDJSON shape. Recommended: each line is a standalone envelope `{data: <envelope>, meta: {command: "events.tail"}}`. Consumers that piped raw NDJSON before get the meta wrapper too — a meaningful schema break worth gating on a follow-up release note.

For (3): wrap the `exitCodeFor`/`run`/`main` error handling so when `--json` is set, errors marshal as `{error: {code, message}}` to stderr. The structured error in `pkg/rpc/handlers/prompt.go` already returns `ErrCodeAgentNotReachable` etc., so the mapping is mostly already in the RPC layer — just needs the CLI to translate.

## Acceptance

- `sextant agents check <uuid> --json` emits `{data: <AgentCheck>, meta:{version:1, command:"agents.check"}}`.
- `sextant events tail subj --json` emits one `{data: <envelope>, meta:...}` per line (or, if we keep raw, a documented exception in `specs/cli/commands.md`).
- `sextant agents show 00000000-... --json` writes `{error: {code:"AGENT_NOT_FOUND", message:"..."}}` to stderr and exits non-zero.
- One regression test per error code in `cmd/sextant/*_test.go`.

## Related

- `[[feat-cli-output-protocol-wiring]]` — parent.
- `[[feat-cli-exit-code-no-results]]` — sibling (exit code 10).
- `pkg/cliout/envelope.go` — the contract.

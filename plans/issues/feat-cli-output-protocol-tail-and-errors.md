---
title: Output protocol — sweep bespoke JSON writers + wrap error paths under --json
status: open
priority: P3
created_at: 2026-05-27T03:20-07:00
labels: [feature, cli, output-protocol, follow-up]
discovered_in: feat-cli-output-protocol-wiring landed the writeJSON sweep but two corners still emit raw payloads — agents_check.go (renderAgentCheck) and tail.go (renderTailEnvelope) — and the --json error paths still surface as plain text
---

## Decision (2026-05-28)

`sextant tail` (and any future bus-passthrough stream command)
**stays raw** as a deliberate, documented exception. Each line is
already a self-describing `sextantproto.Envelope` carrying its
own `proto_version`; wrapping in `cliout.Envelope` would be
envelope-in-envelope without signal. Rationale in
[`plans/rfc-cliout-envelope-role.md`](../rfc-cliout-envelope-role.md):
the wrapper is useful for commands that don't naturally emit JSON
(`--json` is a "make this scriptable" mode); commands that
fundamentally emit data don't need it.

Remaining work is documentation-only:

1. Add a section to `pkg/cliout/doc.go` describing the
   data-emitter exception and naming `sextant tail` as the
   canonical example.
2. Add a note to `specs/cli/commands.md`'s `tail` section
   documenting the exception for operator-facing context.
3. Add a `--raw` is not needed sentence — raw IS the default; no
   flag required.

The error-envelope half is pure execution and could ship anytime; not blocking on input. Splitting into [[feat-cli-output-protocol-error-envelope]] when picked up.

## Progress (2026-05-27)

- `agents_check.go::renderAgentCheck` swept — wraps the AgentCheck in `cliout.WriteEnvelope` (earlier commit).
- **Error-envelope half landed.** Fang's `errorBanner` now checks `globalFlags.asJSON` and routes through `cliout.WriteErrorEnvelope` with a stable code mapped from the error type — sentinel + `client.RPCError` + daemon-unreachable substrings all covered. See `cmd/sextant/errors_map.go` for the mapping table; `errors_map_test.go` pins every entry plus the banner wiring (JSON mode and plain mode tested separately). Fixes Codex finding 2.

Remaining (the open question keeping this ticket needs-input):

1. **`cmd/sextant/tail.go::renderTailEnvelope`** — emits raw envelope NDJSON. **Special case**: tail's NDJSON contract may justify staying raw (it's not a single response, it's a stream). Decide explicitly: either wrap each line in `cliout.Envelope` (NDJSON of envelopes, `meta.command = "events.tail"` repeated per line) or document why tail stays raw.

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

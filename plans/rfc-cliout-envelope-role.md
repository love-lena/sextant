# RFC: `cliout.Envelope`'s role — what it is, where it applies, where it shouldn't

- **Status:** draft, 2026-05-28
- **Author:** session conversation, captured by Claude
- **Resolves:** the open question in `plans/issues/feat-cli-output-protocol-tail-and-errors.md`

## TL;DR

`cliout.Envelope` is the **CLI output protocol** — a stable
`{data, meta}` JSON shape that wraps every `sextant <cmd> --json`
emission so downstream scripts can branch on schema drift via
`meta.version`. It is not the wire protocol. It is not a generic
"wrap everything" wrapper. Streams emitting self-describing
bus envelopes (the only current example is `sextant tail`) should
**stay raw** as a deliberate, documented exception. Adding the
outer wrapper to tail would be envelope-in-envelope without
adding signal.

## The two envelopes (and which is which)

Sextant has two distinct envelopes that operators sometimes
conflate:

### `sextantproto.Envelope` — the **bus protocol**

- Lives in `pkg/sextantproto/envelope.go`.
- Defined by `specs/protocols/envelope-schema.md`.
- Every message on the NATS bus is one of these: trace IDs, span
  IDs, `from` / `to` addresses, idempotency key, `kind`
  discriminator, JSON payload, etc.
- Versioned via `ProtoVersion` (per-envelope field).
- Audience: every component on the bus (daemon, sidecars, CLI as
  bus client) — programmatic consumers.

### `cliout.Envelope` — the **CLI output protocol**

- Lives in `pkg/cliout/envelope.go`.
- The shape:

  ```json
  {"data": <payload>, "meta": {"version": 1, "command": "agents.check"}}
  ```

- The error variant (stderr-only, paired with non-zero exit):

  ```json
  {"error": {"code": "AGENT_NOT_FOUND", "message": "..."}}
  ```

- Versioned via `meta.version` (constant `cliout.EnvelopeVersion`).
- Schema evolution rule lives in `pkg/cliout/doc.go`: additive =
  no bump; rename / remove / reorder / type-change = bump +
  `--meta-version=N` flag opt-in.
- Audience: **scripts and humans piping `--json` output through
  jq**. The contract: scripts can branch on `meta.command` to know
  which command produced this output, on `meta.version` to know
  whether schema drift may have happened, and on `error.code` to
  programmatically handle failures.

## What `cliout.Envelope` is for

The use case it was built for: an operator runs
`sextant agents check $uuid --json` in a CI script, pipes through
`jq '.data.verdict'`, branches on `healthy / degraded / lost`. The
script needs to:

1. Know which command produced this output (in case the same
   script handles multiple shapes — `meta.command`).
2. Detect schema drift (in case sextant ships a `--meta-version=2`
   in a future release — `meta.version`).
3. Handle errors via stable codes, not English text — `error.code`.

`cliout.Envelope` solves all three for the **single-response
case**: one command invocation, one JSON output, one envelope.
That's what `agents check`, `agents list`, `agents show`,
`pending list`, `audit query` (now `audit list`), etc. emit.

## Where it should NOT apply: streams of bus envelopes

`sextant tail` and `sextant ask --stream` emit a sequence of
`sextantproto.Envelope` lines as they flow off the bus. Each line
already carries:

- `id` (envelope UUID)
- `kind` (the discriminator — `agent_frame`, `lifecycle`, etc.)
- `trace_id`, `span_id`, `ts`, `from`, `to`, ...
- `payload` (the kind-specific JSON)

Wrapping each line in `cliout.Envelope` would mean
**envelope-in-envelope**:

```json
{
  "data": {
    "id": "...", "kind": "lifecycle", "trace_id": "...",
    "payload": {"transition": "started"}, ...
  },
  "meta": {"version": 1, "command": "events.tail"}
}
```

The outer `data`/`meta` adds three things:

- `meta.command` — duplicated on every line. Once at the start of
  the stream would be useful; per-line is noise.
- `meta.version` — useful, but the bus envelope already has
  `proto_version` (per-line) that serves the same role for the
  inner payload.
- The `data` indirection — every script piping `sextant tail | jq`
  has to change to `jq '.data | ...'`. Hard schema break, no
  meaningful signal added.

The bus envelope **already is** the self-describing unit for
streaming. `cliout.Envelope`'s job (provide a stable wrapper
where one wouldn't naturally exist) is redundant here.

## Decision rule

**The wrapper is useful for commands that typically don't emit
JSON — `--json` is a "make this scriptable" mode on top of a
human-readable default. The wrapper gives those commands a
structured shape that wouldn't naturally exist.**

**Commands that fundamentally emit data don't need it — the data
already IS the output, and the wrapper would be an extra layer of
indirection without signal.**

Concretely, use `cliout.Envelope` when:

- The command's default mode is human-readable text (terminal
  display, CLI/TUI interaction) and `--json` is the opt-in
  structured mode.
- The payload is command-specific data the CLI synthesized
  (`AgentCheck`, `AgentSummary`, etc.) — not a bus message
  passed through verbatim.

Stay raw when:

- The command IS a data emitter — the stream of data is the
  point of the command (`sextant tail`, future `sextant ask
  --stream` if it lands).
- Each unit is already a self-describing protocol record with
  its own version field.

The set of "data-emitter" commands is small and unlikely to grow —
streaming-bus-passthrough is a deliberately narrow surface, not a
default mode.

## Recommendation for the open question

For `sextant tail --json`: **stay raw**. Document in
`specs/cli/commands.md` and `pkg/cliout/doc.go` that tail (and any
future bus-passthrough stream command) is an explicit exception
because each line is already a `sextantproto.Envelope` carrying
its own `proto_version`. No `--raw` flag needed — raw IS the
default.

If a future operator workflow surfaces a need for command-level
metadata on a tail stream, the right shape is a **one-time header
line** at the start of the stream (a `cliout.Envelope` carrying
just the meta, followed by raw envelope lines). That mirrors the
shape of `git diff --raw` and similar tools. Not worth shipping
preemptively — file a follow-up if the need surfaces.

## Schema evolution recap

`cliout.EnvelopeVersion = 1` today. Bumping requires:

1. Bump `EnvelopeVersion` to N.
2. Add a `--meta-version=N` flag on affected commands. Default
   stays at N-1 for one release.
3. Flip the default after one minor release; keep the flag
   another release.
4. Drop the v1 path two releases later.

Error codes (`CodeAgentNotFound` etc.) are part of the contract.
Adding a code is additive (no bump). Renaming or removing is a
break (bump + flag).

The bus envelope's `ProtoVersion` evolves independently —
schema-breaking changes there land via the
wire-format-negotiation work tracked at
`feat-semver-versioning`'s follow-ups.

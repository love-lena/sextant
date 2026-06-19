# The conformance-vector format

This is the contract that makes a client **co-equal** ([ADR-0041](../../docs/adr/0041-clients-are-co-equal-across-languages.md)).
A conformance vector is a recorded transcript: given an input, a thing the
client does — a convention verb, or the frame codec — produces *exactly* these
primitive bus operations (or these exact bytes), in this order. Every language's
SDK replays the **same JSON files**, recorded once, and a client is co-equal for
a protocol epoch when it reproduces them. This file is the spec an implementer
in any language reads to replay the vectors and capture identical ones.

The vectors are language-neutral and ship with the protocol, under
`protocol/conformance/vectors/`. The Go runner that replays them lives in
`clients/go/conformance/` — see [Why the runner is not in `protocol/`](#why-the-runner-is-not-in-protocol).

## The two vector kinds

### Wire vectors — the frame codec

`protocol/conformance/vectors/wire/*.json`. A frame and its exact canonical
serialized bytes. Each SDK's codec must **encode** the frame to those bytes and
**decode** the bytes to that frame.

```json
{
  "epoch": 1,
  "description": "a chat.message frame and its canonical JSON bytes",
  "frame": { "author": "…", "epoch": 1, "id": "…", "kind": "message",
             "record": { "$type": "chat.message", "text": "hello, bus" } },
  "bytes": "7b22617574686f72…7d"
}
```

- `frame` is the structured frame (the bus-stamped wire wrapper: `id`, `author`,
  `kind`, `epoch`, `record`, and the artifact-only `revision`/`createdAt`/`updatedAt`).
  Stored in canonical JSON.
- `bytes` is the **lowercase hex** of the frame's canonical JSON serialization.
  (The wire form is JSON today; ADR-0016 anticipates DAG-CBOR later, which is a
  protocol-epoch change and a new vector set.)
- The codec is verified in both directions: `decode(hex) == frame` **and**
  `canonical(encode(frame)) == canonical(hex bytes)`. The encode direction is
  what pins cross-language parity — every SDK must serialize the same frame to
  the same canonical bytes.

### Op-transcript vectors — convention behaviour

`protocol/conformance/vectors/<convention>/<verb>.json`. Given a domain input and
a verb, the **ordered** list of primitive bus operations the verb emits.

```json
{
  "epoch": 1,
  "convention": "goals",
  "verb": "setCriterion",
  "description": "…",
  "input": { "goalId": "g1", "criterionId": "c2", "status": "met" },
  "operations": [
    { "op": "artifact.get",    "name": "goal.g1" },
    { "op": "artifact.update", "name": "goal.g1",
      "payload": { "…": "…" }, "expectedRev": 4 },
    { "op": "message.publish", "subject": "msg.topic.goals",
      "payload": { "…": "…" } }
  ]
}
```

Field reference:

| Field | Meaning |
|---|---|
| `epoch` | The protocol epoch this vector is pinned to. A client conforms per epoch. (1-based; `0` means unset and is rejected.) |
| `convention` | The convention the verb belongs to (e.g. `goals`). The runner maps `(convention, verb)` to a registered verb. |
| `verb` | The verb to invoke. |
| `description` | Free text for the reviewer. Not compared. |
| `input` | The domain arguments the verb is called with. Opaque to the format; the verb decodes it. |
| `operations` | The expected ordered primitive ops. |

Each operation in `operations`:

| Field | When present | Meaning |
|---|---|---|
| `op` | always | A primitive operation name that **must** exist in [`methods.json`](../methods.json) (`artifact.create`/`update`/`get`/`list`/`delete`, `message.publish`/`read`/`subscribe`, …). The runner asserts this. |
| `subject` | message ops | The `msg.*` subject. |
| `name` | artifact ops | The artifact name. |
| `payload` | when the op carries a record/args | The record or argument set, compared as **canonical JSON** (below). |
| `expectedRev` | `artifact.update` | The compare-and-set revision the update was issued with. |

Only the fields an operation uses are populated; the rest are omitted. **The set
of populated fields is part of the contract** — a verb that omits a subject is
observably different from one that sets it. The `operations` array is
**order-sensitive**.

## The canonicalization rule

Payloads (and the wire frame) are compared as **canonical JSON**, not raw text.
A TypeScript (or any-language) implementer **must** reproduce this exact rule to
capture byte-identical vectors and to compare correctly:

1. **Object keys sorted** by Unicode code point, ascending, **recursively**.
2. **No insignificant whitespace** — no spaces, no newlines, no indentation.
3. **String escaping is the JSON standard with HTML escaping OFF**: escape
   `U+0000`–`U+001F`, `"`, and `\`; do **not** escape `<`, `>`, `&`. This matches
   JavaScript's `JSON.stringify`. (Go's `encoding/json` escapes `<>&` by default;
   the canonicalizer disables that — `enc.SetEscapeHTML(false)`.)
4. **Numbers in minimal canonical form**:
   - An integer-valued number emits its exact integer digits — no fraction, no
     trailing `.0`, no leading zeros, no leading `+`. So `1.0` → `1`.
   - A large integer beyond IEEE-754 double precision keeps its **exact** digits
     (so `9007199254740993` is not rounded). Do not round-trip integers through
     a float.
   - A non-integer uses the **shortest round-trip** float form (so `1.50` → `1.5`,
     `1e2` → `100`), matching `JSON.stringify`.
   - Transcript payloads are domain records, not arbitrary-precision math.
5. **UTF-8** throughout. Unicode in strings is preserved verbatim (not `\u`-escaped).
6. `null`, `true`, `false` as-is. **Arrays preserve order** (they are
   order-sensitive content, never sorted).

Reference TypeScript:

```ts
function canonical(value: unknown): string {
  return JSON.stringify(sortKeys(value)); // JSON.stringify with no spacer
}
function sortKeys(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(sortKeys);            // order preserved
  if (v && typeof v === "object")
    return Object.fromEntries(
      Object.keys(v as object).sort()                       // code-point sort
        .map(k => [k, sortKeys((v as Record<string, unknown>)[k])]));
  return v;                                                 // scalars: JSON.stringify handles 1.0→1
}
```

The Go implementation (`Canonicalize` in `vector.go`) parses into `any` with
`UseNumber()` and re-encodes with sorted keys, HTML escaping off, and the number
rule above. The two must agree byte-for-byte; the number cases in
`vector_test.go` are the shared fixtures.

`null` is the canonical form of an absent payload, so two operations that both
omit `payload` compare equal.

## Recording and replay

- **Recording client.** A fake client that *captures* the primitive operations a
  verb performs instead of issuing them to a bus. The Go one is `Recorder`
  (`clients/go/conformance/recorder.go`); it implements the same primitive
  surface (`Ops`) the SDK offers, so a verb written against the SDK records
  unchanged. A verb that reads before it writes (the common goal pattern: get the
  goal, mutate a criterion, update) is given prior artifact state with
  `SeedArtifact` — recording setup that mirrors the bus state a verb would find
  live, and does **not** appear in the transcript.
- **Record.** Run `verb(input)` against the recorder → captured ops → serialize to
  a vector. The Go runner exposes a `-update` flag
  (`go test ./clients/go/conformance -update`) that re-records the on-disk
  vectors from the registered verbs, so a deliberate verb change is a
  one-command re-record plus a reviewed diff — never a hand-edit.
- **Replay.** The runner discovers vectors, and for each runs the named verb
  against a fresh recorder and asserts the captured ops equal the vector's
  `operations`, in order, each payload under the canonical rule.

A **TS implementer (TASK-174/175)** honours this same contract: read the JSON
from `protocol/conformance/vectors/`, run the TS verb (or TS frame codec) against
a TS recording client, canonicalize with the rule above, and compare to the
vector — passing the suite is what makes the TS client co-equal.

## The protocol-surface guarantee

Separately from replay, the suite asserts that **every `op` named across all
vectors exists in `methods.json`** — a vector that pinned behaviour against a
call the bus cannot serve would be malformed. This extends the prior
operation-name conformance test (which checks the CLI and MCP surfaces match the
operation *names*) from name-set parity to the transcripts. The CLI/MCP
name-parity tests stay where they are (they guard different surfaces); together
they are the full surface check, not a duplicate.

## Why the runner is not in `protocol/`

The language-neutral **vectors** (JSON) and the **format data types** live under
`protocol/conformance/`. The **runner** imports a convention verb to replay it,
and a verb is a client-side library over the SDK. If the runner lived in
`protocol/`, the protocol would import a client — which `importcheck` forbids
([ADR-0041](../../docs/adr/0041-clients-are-co-equal-across-languages.md): the
bus and protocol never depend on a client). So the split is:

- `protocol/conformance/` — pure vector parsing + canonicalization (no convention,
  no client import). `imports_test.go` enforces it.
- `clients/go/conformance/` — the recorder, the SDK adapter, and the runner
  (`ReplayVectors`, `ReplayWireVectors`), convention-**agnostic**: a convention's
  test package registers its verbs and calls the runner.

## The seam for the real conventions

This ticket (TASK-183) lands the machinery **before** the goals convention exists
(TASK-173). The fixture verb in `clients/go/conformance/runner_test.go` — a tiny
goals-like `setGoal` that emits `artifact.update` then `message.publish` — proves
record + replay end-to-end, and its sample vector lives at
`vectors/fixture/setGoal.json`. The wire sample lives at
`vectors/wire/message-frame.json`.

When TASK-173 lands `conv/goals`, it: registers the real goals verbs in a
`Registry`, drops real vectors under `vectors/goals/`, and calls
`ReplayVectors` — **no runner change**. TASK-174 builds the TS frame codec
against `vectors/wire/`; TASK-175 the TS conventions against `vectors/goals/`.
The fixture vector and its convention can be removed once a real convention has
vectors, or kept as a minimal smoke test.

---
status: accepted
signed_off_by: lena
date: 2026-06-03
---

# Artifacts are Lexicon records

An artifact carries a **Lexicon record** (its `Record` field) — a value in the
AT-Protocol data model, the same content model as a message's `Record`
(ADR-0005, ADR-0006). The two primitives share one record shape: a message is a
lexicon in flight, an artifact is a lexicon at rest (named, versioned,
compare-and-set on revision).

**One content model, deliberately.** Making both primitives lexicon records buys
a single mental model, a single place to attach validation and schema, and a
clean path to AT-Protocol lexicon definitions across the whole surface rather
than only on messages. An artifact stops being an opaque blob the bus can say
nothing about and becomes a structured, introspectable record.

**The data model already covers binary.** A lexicon is not "arbitrary JSON" — it
is the [AT-Protocol data model](https://atproto.com/specs/data-model), whose
types include `bytes`, `cid-link`, and `blob` alongside the JSON-native ones.
That model has two standard encodings, and that is the whole of the binary story
— inline now, optimized later, referenced when large:

- **JSON (v1):** the lossless, human-readable encoding. `bytes` ride inline as
  `{"$bytes": "<base64>"}` — a ~33% expansion we accept for now. sextant stores
  the bytes; there is nothing extra to stand up.
- **DAG-CBOR (planned optimization):** the canonical, compact encoding of the
  *same* data model. `bytes` are a native byte string — no base64, no expansion.
  This changes the encoding, not the record shape or the lexicons, and is the
  on-ramp to content-addressing (CIDs).
- **`blob` references (scale option):** for large binaries, the data model's
  `blob` type — `{"$type": "blob", "ref": {"$link": "<cid>"}, "mimeType",
  "size"}` — points at content-addressed storage (a NATS Object Store tier)
  rather than inlining the bytes at all.

**Writes are validated.** `CreateArtifact` / `UpdateArtifact` reject a value that
is not a non-empty, valid JSON lexicon — fail-loud at the writer, mirroring the
envelope's record check. Full data-model validation (the `$bytes` / `$link` /
`$type` conventions and lexicon schemas) is the later validation seam the
`Lexicon` alias leaves room for.

**Why JSON first.** It is human-readable, universally tooled, and identical to
the message record, so an operator can `nats kv get` an artifact and read it. We
start there and switch encodings / add a blob tier when cost or scale warrants,
without touching the record-shape decision.

This refines the ADR-0006 note about binary "living in an artifact": binary is a
first-class `bytes` / `blob` value *within* a lexicon (inline now, referenced
when large), not raw bytes occupying the whole value.

Map (ADR-0003): the SDK (the Artifacts API and lexicon validation) over the
two-primitives model (ADR-0005).

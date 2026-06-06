---
status: accepted
signed_off_by: lena
date: 2026-06-04
---

# The verb surface is the protocol

Sextant's domain verbs — publish, subscribe, read, the artifact CRUD, the
clients registry — are defined **as the protocol, upstream of the SDK**, not as
the SDK's method set. The Go SDK is the reference client and the best-supported
way to speak Sextant; it is not the definition. The CLI, the MCP server, and any
bring-your-own client in any language are all *conforming clients* over one
written surface. A client built without the SDK needs something to build
against — and that something is the protocol, not our Go code (ADR-0007:
client-first, works against any NATS).

## Two tiers

The protocol is two layers, and only one is enforced:

- **The bus-enforced core** — identity/auth (per-client JWT; `sender` is the
  authenticated name, unforgeable — ADR-0012), the reserved `sx` namespace and
  its guardrails, and the provisioned topology (the `MESSAGES` stream, the
  buckets). A client *cannot* violate this; the bus rejects it.
- **The conventional contract** — the envelope schema, the verb semantics, and
  the lexicon record shapes. The bus permits violations; peers enforce by
  choosing to (a malformed envelope is quarantined by receivers, not refused by
  the bus). Conventions are optional (ADR-0004); this tier is *agreed*, not
  imposed.

The verb surface lives in the conventional tier, resting on the enforced core.

## Four representations, one per layer

Do not force one format across the whole surface. Each layer takes the
representation that fits it:

1. **Lexicons** (the wire envelope + every record shape) — machine-readable
   schemas in the **AT-Protocol lexicon format** (a minimal subset: `record`,
   `object`, `ref`, `union`, `blob` as needed). The shapes proliferate and
   receivers validate against them; this is where machine-readability pays rent
   (ADR-0006, ADR-0016).
2. **Methods** (the verb index) — a machine-readable file, one entry per verb:
   input lexicon, output lexicon, delivery mode. It is **transport-neutral**: no
   NATS subject, stream, bucket, or KV operation appears here (ADR-0013 rule 1:
   no backend type leaks the public API).
3. **Semantic contract** — one page of prose: the behaviour any backend must
   honour (durability, ordering, compare-and-set, client-controlled replay,
   `sender` = authenticated identity, messages enveloped vs. artifacts bare)
   (ADR-0013 rule 2).
4. **NATS binding reference** — prose: how *this* substrate realises each verb
   (the connect handshake; "publish encodes an envelope onto `msg.<subject>` on
   `MESSAGES`"). `pkg/wire` + `pkg/sx` are its Go expression. A bring-your-own
   *NATS* client reads this.

Replacing NATS leaves layers 1–3 untouched and rewrites only layer 4 plus the
SDK's realisation. We add **no `Backend` interface and no per-backend
binding-as-data** until a second backend forces the seam's shape (ADR-0013
rule 3). The verbs port cheaply; the enforced core (JWT identity especially)
ports hard — which is exactly why the seam waits for a real second backend.

## Lexicon format now, NSID authority later

We adopt the AT-Protocol lexicon **shape** and defer the **NSID** (the
reverse-DNS authority). An NSID is `authority + name`; we keep the name and
defer the authority:

- Interim lexicon ids are the future NSID minus its authority — `chat.message`,
  `artifact`, `client`. Records carry `$type` from day one (`$type:
  "chat.message"`); only the value's namespace format migrates later.
- Going public adopts real NSIDs by **prepending a reverse-DNS authority** (a
  mechanical find-replace). Collision-safety between built-in and user record
  types is deferred to that same moment.
- That migration is a **MAJOR** version bump (every record's `$type` string
  changes — observable, and breaking for any consumer that dispatches on it). It
  is **not** a protocol-epoch bump: `$type` lives inside `Record`, the
  free-evolution zone, not the frozen wrapper (ADR-0006, ADR-0010).

User record types are theirs; reverse-DNS is recommended, not enforced
(ADR-0004).

## Why

SDK-as-definition makes "bring your own client" aspirational: there is nothing
to implement against but our Go behaviour. Making the protocol the source of
truth — written, language-neutral, transport-neutral — is what lets a non-Go
client exist at all, what lets the CLI and MCP server be *provably* the same
surface (one machine-readable method index, one conformance test) rather than
two look-alikes that drift, and what keeps the backend swappable at near-zero
cost.

Map (ADR-0003): the SDK's public API (the domain verbs) and the two primitives'
written contract. Sharpens ADR-0013's "one-page semantic contract" into the
four-layer representation above; supersedes nothing.

# The protocol

Sextant's protocol is the **source of truth** — the language-neutral,
transport-neutral definition that the Go SDK, the CLI, the MCP server, and any
bring-your-own client conform to. The SDK is the reference client and the
best-supported way to speak Sextant; it is not the definition
([ADR-0017](https://github.com/love-lena/sextant/blob/main/docs/adr/0017-the-verb-surface-is-the-protocol.md)).

The raw, machine-readable files live in
[`protocol/`](https://github.com/love-lena/sextant/tree/main/protocol); this
chapter embeds them inline, so the rendered spec and the exact definitions sit in
one place.

## Two tiers

Only one tier is enforced:

- **The bus-enforced core** — identity/auth (every client connects as its own
  verified identity; `sender` is the authenticated name, unforgeable), the
  reserved `sx` namespace and its guardrails, and the provisioned topology. A
  client *cannot* violate this; the bus rejects it.
- **The conventional contract** — the envelope schema, the verb semantics, and
  the lexicon record shapes. The bus permits violations; peers enforce by
  choosing to (a malformed message is quarantined by receivers, not refused by
  the bus). Conventions are optional; this tier is *agreed*, not imposed.

## Four layers

Each layer takes the representation that fits it — schemas for data, a neutral
index for the verbs, prose for the contract and the binding.

| Layer | What it is |
|---|---|
| [Lexicons](protocol/lexicons.md) | the wire envelope + record shapes (AT-Proto lexicon format) |
| [The verb surface](protocol/verbs.md) | the domain verbs, transport-neutral (no backend operations) |
| [Semantic contract](protocol/semantic-contract.md) | what any backend must honour |
| [NATS binding](protocol/nats-binding.md) | how the NATS backend realises each verb |

## The wire atom

Every message travels as a JSON **envelope** wrapping a typed record:
`{ id, sender, kind, epoch, record }`. The wrapper core (`id`, `sender`, `kind`,
`epoch`) is **frozen** and protected by the protocol epoch; `record` is the
**free-evolution zone**. `sender` is set from the authenticated identity, never
by the caller; `epoch` is checked on every message, because durable streams
outlive epochs. Messages travel enveloped; artifacts and registry records are
stored **bare** — the lexicon record itself, no envelope. The full definition is
on the [Lexicons](protocol/lexicons.md) page.

## Lexicon ids: name now, authority later

Lexicons use the AT-Protocol lexicon shape, but the **NSID authority is
deferred** (ADR-0017). An NSID is `authority + name`; the ids here are the
*name*, minus the reverse-DNS *authority* — `chat.message`, not
`dev.example.chat.message`. Records carry `$type` from day one; going public
prepends the authority (a MAJOR version bump, not an epoch bump — `$type` lives
in the free-evolution `record`, not the frozen wrapper).

## One surface, many faces

The SDK, the CLI, the MCP server, and any BYO client are all conforming faces
over this one surface. Where it matters that two faces are *provably* the same —
the CLI you test by hand and the MCP tools an agent drives — a conformance test
pins them to [`methods.json`](protocol/verbs.md).

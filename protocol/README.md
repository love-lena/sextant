# `protocol/`

Sextant's language-neutral protocol surface — the source of truth for what the bus
implements. Sextant **is the bus**: it implements the protocol's operations over a
pluggable backend, and the protocol is invariant across backend swaps (ADR-0018).
The SDKs are conveniences over the Wire API; they conform to these files and do not
define the protocol by themselves.

## Files

| File | Role |
|---|---|
| `lexicons/*.json` | The record shapes and the **frame** — the bus-stamped wire wrapper around a record. |
| `methods.json` | The **operations** index — the calls a client makes to the bus. |
| `semantic-contract.md` | **The backend interface** — what any backend module must provide so the bus can implement the operations. |
| `nats-binding.md` | The **NATS module** notes — how the NATS backend satisfies the interface. Internal. |
| `conformance/` | The **conformance vectors** — recorded operation transcripts every client replays to prove co-equality (ADR-0041). `FORMAT.md` is the cross-language spec; `vectors/` holds the JSON. |

## How to read it

A client makes a **call** over the Wire API; the bus serves it, stamps the frame,
and enforces identity. The record is user space (the client supplies it); the frame
is bus space (the bus produces it).

- To understand the surface a client calls: `methods.json` for the operations,
  `lexicons/` for the records and the frame.
- To understand the guarantees the bus relies on: `semantic-contract.md` — the
  backend interface.
- To add a second backend: implement `semantic-contract.md` as a new backend module
  and write its own module notes (as `nats-binding.md` does for NATS).

## Rules

- **NATS is internal.** It never appears in client-facing docs — only in
  `nats-binding.md`, the NATS module's own notes. Clients call the bus; the backend
  is opaque to them.
- Lexicon ids currently omit a reverse-DNS authority. Records still carry `$type`;
  adding a public authority later is a public API version change.
- Use **topic** for bus rooms such as `msg.topic.<name>`. **Channel** is reserved
  for harness-specific push mechanisms.

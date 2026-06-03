---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# The wire atom

Every message is a JSON envelope wrapping a typed record:

```jsonc
{ "id": "01J…", "sender": "…", "kind": "message", "epoch": 1,
  "record": { "$type": "app.sextant.…", "…": "…" } }
```

- **`id`** — a ULID set by the sender; also the dedup key.
- **`sender`** — the authenticated identity (see the authn ADR).
- **`kind`** — the *frame* type; `"message"` for now (room for other frames later).
- **`epoch`** — the protocol version this was written under, checked *per message*
  because durable streams outlive epochs.
- **`record`** — the typed content: an **AT-Protocol lexicon**, *by convention
  only* for now (the format, with no validation or codegen yet). `record` is
  always JSON; binary lives in an **Artifact**, referenced from the record.

Correlation fields (`correlation_id`, `reply_to`, `workflow_id`) live **inside the
record**, not the envelope — the wrapper stays minimal. The trustworthy timestamp
is **bus-stamped** (JetStream metadata), not a sender field.

The `id` ULID embeds a sender-set millisecond timestamp, so it is not a trusted
clock. The SDK enforces `|ULID.ts − bus-stamped ts| ≤ skew_tolerance` (default
5 min): the **sender** checks before publishing and the **receiver** checks on
consume, quarantining + flagging violators. There is no central enforcer — each
endpoint validates locally against the bus clock, the way TLS endpoints validate
against their own — so a wildly-false ULID time cannot survive a compliant
reader, while the bus-stamped time stays the precise authoritative clock. (A soft
clock-skew *announcement* also rides the connect handshake — see lifecycle &
versioning.)

Why JSON and no codegen. Readable (`sextant tail` shows real messages), polyglot,
hackable, zero build step — and since records are content-opaque to the bus, there
is nothing to schema-enforce anyway. The envelope **wraps**
the payload (a self-contained object) rather than using NATS headers, so it stays
portable across backends.

Why AT-Proto lexicons, by convention. A cheap format commitment now buys future
option-value (drop-in validation tooling; ecosystem interop) — an owned,
deliberate non-YAGNI bet. The frozen wrapper core (`id`/`sender`/`kind`/`epoch`) is
the only thing an epoch bump protects; `record` is the free-evolution zone.

Map (ADR-0003): the SDK / wire (the client ↔ bus connection) and Messages.

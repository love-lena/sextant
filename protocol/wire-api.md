# The Wire API

The Wire API is the **call** protocol between a client and the bus: a client invokes
an operation and receives its result. It is **backend-neutral** — every backend
module satisfies the same operations (`semantic-contract.md`), and the NATS
realization is internal (`nats-binding.md`). This page is the contract an SDK
implements; `methods.json` is the operation index it implements against.

## A call

A **call** is client↔bus: the client names an operation, supplies an input record,
and receives the result. It is distinct from **request/reply**, which is
client↔client — one client sending a message to another and getting a reply. The
Wire API is only the client↔bus path.

## The bus stamps the frame

The client supplies only the **record** (user space). The bus stamps the **frame**
(bus space) from the authenticated connection: `id` (a ULID, also the dedup key),
`kind`, `epoch`, and `author`. An SDK must **never** set `id`, `author`,
`revision`, or timestamps — they are the bus's to produce, and `author` cannot be
forged by editing the record. See [the frame](frame.md).

## Delivery shapes

Each operation's result takes one of three shapes (declared per operation in
`methods.json`):

- **one-shot** — returns once.
- **pull-batch** — returns a batch plus a cursor to continue. Cursor `0` is the
  beginning of retained history; passing the returned `next_cursor` unchanged
  continues with no gaps and no duplicates. The backend keeps no per-reader
  position — the bus chooses the start on each read, on the client's behalf.
- **push-stream** — delivers frames until the client stops.

## The connect handshake

Before it serves calls, the bus runs a handshake (ADR-0008, ADR-0010, ADR-0020).
Its shape is backend-neutral:

1. **Authenticate** the connection. The client id is read from the credential, not
   asserted by the client — so what a client claims and what the bus authenticated
   cannot diverge.
2. **Confirm the identity** is known — issued and not retired — else refuse to
   proceed.
3. **Hard-gate the epoch**: refuse unless the bus epoch exactly matches the
   client's. The epoch is re-checked on every frame, because retained streams can
   outlive a protocol epoch.
4. **Soft clock-skew check** against the bus's server time — an announce, not a
   refusal.

**Presence is derived from the live connection**, not written at connect. A dropped
connection alone never ends a client; a clean close goes offline without retiring.
Retire (`clients.retire`) is a deliberate decommission, distinct from a disconnect.

## Identity issuance

`clients.register` is the single call a process makes without a pre-existing client
identity. It is authorized by the **caller's authority** — either a held identity
(an operator minting for another) or a bootstrap/enrollment authorization (minting
for self) — and returns a minted id plus its credential. The bus is the sole
minter; the signing key never leaves it.

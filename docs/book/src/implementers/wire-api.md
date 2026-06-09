# The Wire API

> 🚧 **Claude outline — TODO for Lena.** This is the backend-neutral call contract
> a second SDK implements — prose, yours to write. Delete this banner when done.
>
> When you write it, the backend-neutral material currently inlined in
> `protocol/nats-binding.md` (the handshake steps, delivery shapes) should be
> factored out into this page's canon and `nats-binding.md` trimmed to NATS
> specifics. (I drafted a version of this and pulled it back — it's prose, so it's
> yours; the draft is in the PR history if useful.)

Suggested coverage:

- Audience: building a **second SDK** (e.g. TypeScript) or tooling that speaks the
  protocol directly.
- A **call** = a client invokes an operation on the bus and gets a result
  (client↔bus) — distinct from request/reply (client↔client).
- The bus stamps the frame and `author` from the authenticated connection; an SDK
  must **never** set `id`, `author`, `revision`, or timestamps.
- **Delivery shapes**: one-shot · pull-batch (cursors; no per-subscriber state) ·
  push-stream.
- The **connect handshake** (authenticate → confirm issued/not-retired → hard-gate
  epoch → soft clock-skew); presence is derived from the live connection.
- **Identity issuance**: `clients.register` is the one call made without a
  pre-existing identity (operator-mints or self-enroll); the signing key never
  leaves the bus.
- Out of scope here: backend specifics → `nats-binding.md` (internal).

# The Wire API

> 🚧 **Claude outline — TODO for Lena.** The bullets below are suggested coverage,
> not finished copy. Replace this page with prose and delete this banner.
>
> **Note for TASK-32.4:** this page must stay **backend-neutral**. The
> NATS-specific binding stays in `protocol/nats-binding.md` (internal); the
> transport description gets factored out of it into canon for this page.

- Audience: you're building a **second SDK** (e.g. TypeScript) or tooling that
  speaks the protocol directly.
- A **call** = a client invokes an operation on the bus and receives its result
  (client↔bus) — distinct from request/reply (client↔client).
- The request/response shape, abstractly: operation name + input record; the bus
  stamps the **frame** and `author` from the authenticated connection.
- **Delivery shapes**: one-shot (returns once), pull-batch (cursors — no
  per-subscriber state on the backend), push-stream (frames until stopped).
- What an SDK must **not** set: `id`, `author`, `revision`, timestamps — all bus
  space.
- The epoch handshake (`clients.hello`) and the connect requirements.
- Out of scope here: any backend specifics → see `nats-binding.md` (internal).

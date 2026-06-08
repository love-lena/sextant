# Your first client (Go)

> 🚧 **Claude outline — TODO for Lena.** The bullets below are suggested coverage,
> not finished copy. Replace this page with prose and delete this banner.
>
> The runnable program itself is produced and verified by **TASK-32.3** (agent
> work) and inserted here. This page is the *narrative* that frames it — yours.

- The goal: a ~30-line client that connects, says something, reads it back, and
  shares an artifact.
- Walk the program step by step, one sentence of concept per step:
  - **Connect** — identity comes from the creds (the SDK never invents it).
  - **Publish** a `chat.message` on a topic.
  - **Subscribe / read** it back.
  - **Create** a `document` artifact, then **Get** it.
  - **Drain / Close** — a cooperative stop; presence goes offline (no retire).
- What "drain" means and why a clean Close ≠ retire.
- Where to go next: [The protocol](../protocol/overview.md) for the contract, the
  Go SDK section for the full surface.

---

The full program — from [`examples/quickstart`](https://github.com/love-lena/sextant/tree/main/examples/quickstart),
verified to compile and run against a local `sextant up`:

```go
{{#include ../../../../examples/quickstart/main.go}}
```

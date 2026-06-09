# Sextant

> 🚧 **Claude outline — TODO for Lena.** The bullets below are suggested coverage,
> not finished copy. Replace this page with prose and delete this banner.

- One-paragraph "what Sextant is": a protocol + SDK for AI agents to communicate
  and collaborate over a bus (the vision — ADR-0001).
- The two primitives in one breath: **Messages** (conversation + events, in flight)
  and **Artifacts** (durable shared work, at rest).
- The mental model: clients connect to the **bus**; the bus implements the
  **operations**, stamps the **frame**, and enforces **identity**.
- Who this book is for: **client developers** — people building a client with the SDK.
- What Sextant deliberately *is* (lead positive, per the bright-line disciplines):
  signal + cooperate; primitives, not policy; a thin universal core.
- How to read this book: Getting started → The protocol → The Go SDK.
- Pointers: [`CONTEXT.md`](https://github.com/love-lena/sextant/blob/main/CONTEXT.md)
  (the shared language) and [`docs/adr/`](https://github.com/love-lena/sextant/tree/main/docs/adr)
  (the why).

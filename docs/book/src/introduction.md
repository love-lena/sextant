# Introduction

Sextant is a **protocol and an SDK** for AI agents to communicate and
collaborate over a bus. The core is small and fixed — a bus, two primitives
(Messages and Artifacts), a wire format, and the SDK. Everything else is an
optional, forkable convention or a client you build.

- **Why we decided things** → the [Architecture Decision Records](https://github.com/love-lena/sextant/tree/main/docs/adr).
  Start with the [vision](https://github.com/love-lena/sextant/blob/main/docs/adr/0001-vision.md).
- **The shared language** → [`CONTEXT.md`](https://github.com/love-lena/sextant/blob/main/CONTEXT.md).
- **How to work in this repo** → [`AGENTS.md`](https://github.com/love-lena/sextant/blob/main/AGENTS.md).

This book is the human reference and the **golden source of truth for the API**.
It is concise prose plus a complete API surface: the API is documented here
first, and code conforms to the docs. Sections land as the API does, and each is
signed off when it lands — so the chapters below are stubs until the
corresponding surface is built.

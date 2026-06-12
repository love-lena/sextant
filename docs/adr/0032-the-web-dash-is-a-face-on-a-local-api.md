---
status: proposed
date: 2026-06-12
---

# The web dash is a face on a local API

The dash is a terminal cockpit today ([ADR-0023](0023-the-dash-is-a-composable-pane-cockpit.md),
[ADR-0024](0024-the-dash-is-three-master-detail-browsers.md)): a TUI client
holding one bus identity. A web (browser) dash is the natural next face on the
same data. The question this ADR settles is *where the browser meets the bus*.

A browser cannot safely hold a bus credential. The bus authenticates each
client with a long-lived NATS credential (ADR-0012, ADR-0020); shipping one into
page JavaScript exposes it to exfiltration (XSS, the browser's own storage), and
there is no short-lived browser-scoped credential model yet. Pointing the browser
straight at the bus would also bind the UI to the wire protocol, so every UI
change would be a protocol client.

## The decision

`sextant dash --serve` runs the dash's **one** bus identity as a **local HTTP
API on 127.0.0.1**, and the browser talks only to that API. The Go process stays
the single bus client; the credential never leaves it. The browser holds no bus
credential and speaks no NATS — it makes ordinary HTTP calls to loopback.

**The API contract is the stable boundary; the UI is a swappable face on it.**
The API is deliberately shaped and is what any frontend depends on:

- REST/JSON reads that mirror the CLI read operations one-for-one — `/api/self`,
  `/api/clients`, `/api/messages`, `/api/artifacts`, `/api/artifacts/{name}` —
  returning the same SDK structs the CLI's `--json` emits, so the API is a
  faithful mirror (and a demo can cross-check it against the CLI).
- A `POST /api/publish` command (the bus owns frame stamping and the
  subject-space check, as ever).
- A live stream, `GET /api/stream`, as **Server-Sent Events**: the push is
  one-directional (bus → browser), which is exactly what a live feed needs, and
  SSE is `net/http` with no new dependency. A WebSocket would add a dependency
  and a bidirectional channel the feed does not use.

The served frontend is itself swappable. The built-in page is a **zero-design
debug surface** — raw HTML/JS, no theme, no layout opinion — which is both the
end-to-end verification harness for the API and a clean, opinion-free baseline so
the intentionally-designed UI is built fresh rather than by fighting a styled
starting point. `--ui <dir>` serves a custom frontend instead.

Access control is local-first and cheap: the listener binds loopback only; every
`/api` call must carry a **per-launch token** (a fresh random secret printed in
the URL at startup, as a Bearer header or `?token=`); and a configurable
**allowed-origin** check (localhost always allowed) lets a separate dev server
host a UI during development while rejecting foreign browser origins.

This is a client-side face, an **opinionated reference-implementation feature**
over the locked core: it touches neither the wire protocol nor the bus's
operation set (no epoch bump — ADR-0010). The dash is just a client (ADR-0014),
and `--serve` is a second way to run it.

## Consequences

- The browser-credential problem dissolves: credentials stay in the process, and
  the browser is a thin consumer of a local API. The blast radius of the served
  surface is the per-launch token on loopback.
- The API contract becomes the thing to keep stable and version deliberately;
  multiple UIs (the debug surface now, a designed UI later) depend on it. This is
  the seam, not an afterthought.
- Delivery splits cleanly. **D1 (this):** the API server, the guards, the
  zero-design debug surface, and a one-command self-validating demo that boots a
  throwaway bus, asserts API/CLI parity and the live stream, and is the
  acceptance test. **D2 (later, separate pass):** the intentionally-designed UI,
  built fresh on the now-verified API, kicked off by its own design conversation
  — no design decisions are baked into D1.
- The local API server is the same seam a future *remote* path would occupy: a
  gateway minting short-lived, scoped browser credentials (or a browser NATS
  WebSocket connection) collapses into this binary's boundary rather than
  reworking it. The MVP is that gateway, local and credential-free.

See TASK-68. Builds on ADR-0023/0024 (the dash) and ADR-0014 (the dash is a
client); orthogonal to the bus protocol.

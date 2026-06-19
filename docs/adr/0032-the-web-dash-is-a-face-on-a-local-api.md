---
status: accepted
date: 2026-06-12
---

# The web dash is a face on a local API

> **Revised by [ADR-0041](0041-clients-are-co-equal-across-languages.md)** — its core stance, *the browser never touches the bus*, is reversed: the dash becomes a direct NATS-WebSocket co-equal TS client (decided in task-179, built in task-180), with a bus WebSocket listener and dash-minted browser credentials. Read this as the history of the local-API boundary, not the current design.

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

## Update — loopback is token-free (TASK-115, 2026-06-15)

The per-launch token is dropped for **loopback** peers (127.0.0.0/8, ::1): a
request from loopback is authorized without a token; a non-loopback peer still
must present it. The `--serve` listener is loopback-bound (there is no host
knob), and loopback is host-bound and implicitly trusted — the posture OAuth
takes for native-app loopback redirects — so the token's CSRF/remote barrier adds
nothing for a local peer, while forcing operators to copy a fresh `?token=` on
every restart (it rotates per launch). The token path stays for any future
non-loopback bind.

Stated plainly: because the listener is loopback-only today, this makes the dash
effectively token-free (the URL is just `http://127.0.0.1:8765/`). The exception
trusts the peer IP, so anything that lets a *non-local* client reach the listener
as loopback — a reverse proxy or SSH tunnel forwarding to 127.0.0.1 — would
bypass the token. The dash is not tunnel-exposed today (the cross-machine tunnel
forwards the NATS bus, not this HTTP API); exposing it later must re-introduce
real auth rather than rely on this exception.

See TASK-68 (and TASK-115). Builds on ADR-0023/0024 (the dash) and ADR-0014 (the
dash is a client); orthogonal to the bus protocol.

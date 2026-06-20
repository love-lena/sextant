---
status: proposed
date: 2026-06-19
---

# The browser dash is a direct NATS-WebSocket co-equal TS client

The browser dash is a co-equal TypeScript bus client. It connects to the embedded
NATS bus directly over `ws`/`wss` using `@sextant/sdk` and the lexicon-defined
convention libraries (`@sextant/conv-goals`, `@sextant/conv-review`), authenticated
by a short-lived, dash-minted, scoped credential. The Go dash process is reduced to
two jobs: serve the static SPA, and mint that credential. This realises
[ADR-0041](0041-clients-are-co-equal-across-languages.md) for the browser — it
joins the pi harness ([ADR-0043](0043-the-pi-harness-is-a-first-class-bus-client.md))
as a first-class non-Go client — and was decided in TASK-179 (ADOPT, sequenced
after the pi client) and built in TASK-180.

## What it reverses

[ADR-0032](0032-the-web-dash-is-a-face-on-a-local-api.md) settled where the browser
meets the bus on the constraint that *a browser cannot safely hold a bus
credential* — there was no short-lived, browser-scoped credential model, so the Go
process held the one identity and the browser spoke only to a local `/api/*` face.
That constraint no longer holds: mint-on-behalf
([ADR-0033](0033-a-dispatcher-mints-its-own-workers.md)) plus a JWT expiry give the
browser its own scoped credential with a built-in cleanup. So the
browser now connects to the bus directly, and ADR-0032's local-API boundary becomes
history. [ADR-0034](0034-the-web-cockpit-rests-on-conventions-not-new-protocol.md)'s
thesis — conventions, not new protocol — holds unchanged and is in fact carried
further: the proof-filter (goals) and the review read-merge-CAS, which used to run
as Go handlers behind `/api/*`, are now the TS convention libraries the browser
runs itself, pinned to their Go peers by the conformance vectors. No bus operation
is added.

## The browser-credential model

The bus exposes a loopback WebSocket listener, default-off and enabled per
deployment (`sextant config set ws-listen 127.0.0.1:<port>`), following the
leaf-listener precedent ([ADR-0038](0038-a-remote-box-joins-through-a-leaf-node.md)): it is
loopback-only and carries no TLS of its own, sitting behind the operator's secure
transport (loopback, SSH-R, Tailscale, WireGuard). The dash — a top-level registered
client, so it may dispatch — mints each browser tab a child credential over
`clients.register`. That credential is scoped by the per-client allow-list to the
child's own call and delivery space: it cannot mint, cannot retire, cannot read
another client's traffic. Custody is simple and short-lived: the credential rides
the dash's own loopback, token-gated HTTP response to the page, and the page opens
its WebSocket with it. Because the dash cannot retire a credential, cleanup is a
short JWT expiry (default one hour, overridable) — the `kind:"browser"` mint is the
one mint the bus bounds with a TTL; every other credential stays perpetual,
unchanged. A tab whose credential lapses is rejected on reconnect and the SPA
surfaces a reload prompt rather than dying silently.

The security posture is deliberately local-first for this iteration: a loopback
`ws://` listener behind the operator's existing secure transport, the same posture
the leaf listener takes. Native `wss` TLS on the listener, and garbage-collection of
expired browser client records from the directory, are named follow-ups — neither is
load-bearing for the local-host dash this ADR ships.

## Consequences

The Go dash backend no longer relays or re-implements any bus primitive: the goals
projection, the review verdict path, the message/artifact/client reads, the publish
path, and the live stream all move to the browser over its own Client. This removes
a whole class of "the dash and another client disagree" bugs, because there is one
home per convention rather than a Go re-implementation behind the relay. The cost is
a browser bundle that vendors the SDK browser entry, the conventions, and the
WebSocket transport — built at `make ui` time, no runtime CDN — and a credential that
now reaches page JavaScript, which is why it is short-lived and narrowly scoped.

This ADR is signed off at the m6 → main merge; on sign-off its status and ADR-0032's
and ADR-0034's revision banners flip to reference this as the current design.

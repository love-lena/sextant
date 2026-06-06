# Demos

Recorded walkthroughs of sextant, driven through the operator CLI against a
real, live bus. Each `.tape` is the source script; the `.gif` next to it is the
rendered recording. They are produced with [VHS](https://github.com/charmbracelet/vhs).

## The M2 collaboration loop

![The M2 collaboration loop](m2-collaboration-loop.gif)

Two clients — `alice` and `bob` — collaborate **through** the bus. Nothing talks
directly: the bus is the sole minter of identities (ADR-0020), stamps every frame,
and derives presence from the live connection. This is the scenario the M2
definition-of-done codes against ([`tests/e2e/m2-acceptance.md`](../../tests/e2e/m2-acceptance.md)).
The recording walks the whole loop:

1. **Start the bus** — one process implements the protocol; the signing keys never
   leave it.
2. **Issue identities, two auth modes** — the operator mints `alice`
   (held-identity mode); `bob` self-enrolls on the same box (bootstrap/locality
   mode). Both are `clients register`; the difference is only how the request is
   authorized.
3. **Subscribe** — `bob` subscribes to a topic for live delivery.
4. **Publish with an unforgeable author** — `alice` publishes; the frame lands on
   `bob`'s subscription stamped with `alice`'s bus-minted id. `alice` cannot stamp
   it with anyone else's — the per-client allow-list forbids it.
5. **Share an artifact** — `alice` drafts `the-plan`; `bob` revises it with a
   compare-and-set on the revision (one author at a time); a stale write is rejected.
6. **List clients** — the live directory, with presence derived from the
   connection (no heartbeat): both online.
7. **Durable identity across reconnect** — `bob` disconnects (his identity persists,
   now `offline` — it is not reaped); he reconnects with the same creds under the
   **same id**, flipped back `online`.
8. **Retire** — the operator decommissions `bob` for good; he is gone from the
   directory.

## Regenerating

The recording needs the `sextant` binary on disk. Build it, then render:

```sh
go build -o /tmp/sxdemo/sextant ./cmd/sextant
vhs docs/demos/m2-collaboration-loop.tape   # from the repo root
```

The tape's hidden setup block defines a `sextant` shim and a scratch store dir so
the recorded commands read cleanly — but everything shown being typed is a real
invocation against a live bus.

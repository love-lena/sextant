# Demos

Recorded walkthroughs of sextant, driven through the operator CLI against a
real, live bus. Each `.tape` is the source script; the `.gif` next to it is the
rendered recording. They are produced with [VHS](https://github.com/charmbracelet/vhs).

## The M2 collaboration loop

![The M2 collaboration loop](m2-collaboration-loop.gif)

Two clients — `alice` and `bob` — collaborate **through** the bus. Nothing talks
directly: the bus mints each client's identity, stamps every frame, and serves
the protocol operations. The recording walks the whole loop:

1. **Start the bus** — one process implements the protocol over a pluggable backend.
2. **Mint two clients** — the bus assigns each its own trusted ULID identity.
3. **Subscribe** — `bob` subscribes to a topic for live delivery.
4. **Publish** — `alice` publishes; the frame lands on `bob`'s subscription live,
   stamped with `alice`'s identity (the author is the bus-verified ULID, not a
   value the client supplied).
5. **Read** — the same message read back as a full frame shows what the bus
   stamped: `id`, `author`, `kind`, `epoch`, and the record.
6. **List clients** — the live directory of who is connected right now
   (ULID + display name).
7. **Share an artifact** — `alice` drafts `design-notes`; `bob` revises it with a
   compare-and-set on the revision (one author at a time); `alice` reads back
   `bob`'s revision.

## Regenerating

The recording needs the `sextant` binary on disk. Build it, then render:

```sh
go build -o /tmp/sxdemo/sextant ./cmd/sextant
vhs docs/demos/m2-collaboration-loop.tape   # from the repo root
```

The tape's hidden setup block defines a `sextant` shim and a scratch store dir so
the recorded commands read cleanly — but everything shown being typed is a real
invocation against a live bus.

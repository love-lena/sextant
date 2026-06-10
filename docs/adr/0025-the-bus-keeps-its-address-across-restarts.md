---
status: proposed
date: 2026-06-09
---

# The bus keeps its address across restarts of the same store

When `sextant up` restarts against a store that already has a `bus.json`
discovery file, the bus binds the **same port as before** — provided that port
is still free on the host. The address the store recorded is the address the
store reuses.

## What this guarantees

**Same store ⇒ same address (when available).** A client context whose URL was
pinned at enrollment time (ADR-0021) stays valid across restarts of the same
store without any manual intervention. The zero-config first-run path in the
dash (ADR-0024) — enroll on first start, connect silently on every later start —
holds across bus restarts as well, because the bus comes back on the same port
the context recorded.

**Fallback is automatic and loud.** If the recorded port is genuinely occupied
by something else (a second bus started from a copy of the same store, a long-
lived process that claimed the port between restarts), the bus falls back to a
fresh ephemeral port and prints a one-line notice to stderr:

```
bus: recorded port <N> is in use; starting on a new port (enrolled contexts may need updating)
```

The bus always comes up. The notice makes a subsequent "nats: no servers
available" diagnosable rather than mysterious.

**A fresh store is unchanged.** When no `bus.json` exists yet (first ever
start, or a deliberately clean store), the bus picks any available ephemeral
port exactly as before.

**An explicit `--port` wins.** If the caller passes a non-zero `--port`, that
value is used unconditionally — stable-port resolution is a default, not an
override of an explicit request.

## Why

Enrolled client contexts pin the enrollment-time URL (ADR-0021 resolution
order: a context's URL beats store discovery). This is correct for the general
case — a context can legitimately point at a remote bus — but it means a port
rotation strands every context until it is hand-edited. For a local bus that
binds an ephemeral port, "same store" already implies "same identity, same
JetStream data, same client directory": keeping the address stable closes the
loop and makes the store genuinely self-contained across its lifetime.

## Implementation

`bus.Start` calls `stablePort` before creating the NATS server. `stablePort`
reads the previous URL from the store's `bus.json` (via `pkg/conninfo`),
extracts the port, and probes it with a short `net.Listen` to confirm it is
free. If free: that port is passed to NATS. If taken: −1 (ephemeral) is passed
and the notice is printed. An absent or unparseable `bus.json` returns −1
silently.

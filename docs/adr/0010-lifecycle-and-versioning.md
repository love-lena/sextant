---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# Lifecycle & versioning

**Stopping is cooperative.** A `control.drain` broadcast asks clients to wind
down; the SDK's default handler finishes in-flight work, leaves the clients
registry, and exits cleanly. It's overridable, and a *dropped connection alone
never exits* — the SDK reconnects, so only an explicit drain ends a client.
`sextant up` emits the drain as it shuts down, so closing the bus empties the
room. **Restarting** a client is its launcher's job (systemd / compose / you);
**upgrading** is `sextant down` → rebuild → `sextant up`, which brings the whole
set back together.

**One version number: the protocol epoch.** It lives in a well-known KV key,
written at bootstrap. On connect the SDK reads it and **must match exactly**, or
it refuses with a clear, actionable error — fail-loud, fail-early. The epoch also
rides every message (durable streams outlive epochs) and each client's registry
record (so drift is visible). It bumps **only on a breaking wire change**;
additive changes never bump it, so routine evolution needs no flag-day. Because
every client matches the bus's epoch, all clients are transitively compatible.

**Clock skew, announced at connect.** The SDK already writes a registry record on
connect, and that record's **bus-stamped timestamp is "bus now."** The SDK
compares it to the local clock and, if the skew exceeds tolerance, **announces a
warning** — *"your clock is N off the bus; messages may be rejected, sync NTP."*
This is a soft, early heads-up, **not** a gate: the hard gate is per-message (the
ULID-timestamp check in the wire-atom ADR). So a skewed client learns at the
door, and is enforced at every message.

Map (ADR-0003): SDK (epoch + clock check), the bus (drain), Clients registry.

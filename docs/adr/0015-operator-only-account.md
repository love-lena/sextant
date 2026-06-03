---
status: proposed
signed_off_by:
date: 2026-06-03
---

# Operator-only state lives in its own account

This refines [ADR-0012](0012-reserved-namespace-and-authn.md), which reserved an
`sx_system` bucket for operator-only state. We've chosen a cleaner home for that
state, because a same-account KV bucket can't actually be kept operator-only.

**A reserved bucket can't be guarded by deny-lists.** A NATS KV bucket is backed
by a JetStream stream (`KV_<bucket>`), reachable through the JetStream API —
`$JS.API.STREAM.MSG.GET.KV_sx_system`, consumer creation, and so on. Denying a
client the `$KV.sx_system.>` data subjects leaves those API paths open, so a
client-tier credential could still read or write the bucket. Closing every such
path by enumeration is exactly the fragile deny-list the rest of the design
avoids.

**Operator-only state goes in a separate NATS account.** A NATS account is a
hard, enumerate-nothing boundary: a client in the clients account cannot see
another account's streams or subjects, by construction. So when operator-only
state actually exists, it lives in its own operator account — the same mechanism
that later lets clients own their own buckets without colliding with the
reserved namespace.

**v1 provisions no operator-only bucket.** The only system datum so far — the
protocol epoch — is *public* (clients read it at connect), so it lives in a
client-readable bucket (`sx_meta`), not a protected one. With no operator-only
bucket present there is nothing for a client to reach, and the client guardrail
reduces to the two robustly enforceable rules: deny publishing the `sx.control.*`
subjects, and deny stream/bucket lifecycle. The separate operator account is
added the day real operator-only state appears.

Map (ADR-0003): the bus and the SDK (authn), guarding the reserved `sx`
namespace.

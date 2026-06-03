---
status: accepted
signed_off_by: lena
date: 2026-06-02
---

# The reserved `sx` namespace, and authn

> **Partially superseded by [ADR-0015](0015-operator-only-account.md):** the
> operator-only `sx_system` bucket described below is out of date — operator-only
> state lives in a separate NATS account, and v1 provisions no such bucket.

**Authn yes; dynamic authz deferred.** Every read and write is attributable to an
**authenticated identity** — that is the P0 guarantee. Among authenticated
clients access is otherwise open, with one exception: a **static guardrail around
the reserved `sx` namespace**. A general *per-resource* authz policy is left for
later, and the identity layer is shaped to leave room for it.

**Reserved root: `sx`.** Bootstrap claims a single namespace — `sx.>` subjects and
`sx_`-prefixed KV buckets (`sx_clients`, `sx_workflows`, `sx_system`). The rule is
one line: **the `sx` namespace is Sextant's; everything else is yours.** Two
prefixes because NATS forbids dots in KV bucket names: subjects are dotted
(`sx.control.*`), buckets are underscored (`sx_system`). Reservation is not a
requirement — the buckets are operator-owned furniture, created at bootstrap, and
using them is opt-in.

**The guardrail is part of authn, enforced from day one** — not convention, not
deferred. Because `sextant up` embeds NATS, bootstrap stands the `sx_` buckets up
under an **operator** credential and hands clients a **client** credential whose
fixed permission denies the whole namespace except a small allow-list. It's the
same permission on every client (a constant, not a per-resource policy), so it
needs nothing beyond the authenticated connection we already require. Two static
tiers, and the line between them:

- **Operator** owns `sx_` **bucket lifecycle** (create / reconfigure / delete) and
  `sx.control.*` and `sx_system`. Clients are denied all of it — a client can
  never create, alter, or destroy an `sx_` bucket, nor touch the system bucket or
  control subjects.
- **Clients** may do exactly two things under `sx`: **PUT keys into `sx_clients`**
  (their registry record + heartbeat) and **`sx_workflows`** (workflow state), and
  **publish `sx.workflow.<id>.control` / `.events`**. Everything else under `sx`
  is denied.

The **one** thing genuinely deferred to per-client *distinct* credentials is
**write-precision**: scoping a client to its *own* rows (only `sx_clients/<self>`)
rather than any client's. Until distinct creds land, a client can overwrite
another client's registry record — a narrow gap, not namespace squatting. The
coarse namespace is locked on day one; per-row ownership is the later refinement.

Map (ADR-0003): the SDK (authn at connect) and the bus (the two static credential
tiers + subject/bucket permissions) guarding the reserved `sx` namespace.

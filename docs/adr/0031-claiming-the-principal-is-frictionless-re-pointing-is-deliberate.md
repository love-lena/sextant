---
status: proposed
date: 2026-06-12
---

# Claiming the principal is frictionless; re-pointing it is deliberate

[ADR-0030](0030-clients-act-on-a-principals-messages-as-operator-input.md) made
the principal load-bearing: its messages are operator-equivalent to every
client. It left the principal as a single key, defaulted at bootstrap to the
reserved operator seat, and re-pointed by an operator-credentialed
`principal set <ulid>`. Two things about *how the designation moves* turned out
to want sharpening, and they pull in opposite directions:

- **The first human seat should become the principal on its own.** After
  `sextant clients register --self`, the designation stays the bootstrap
  default (`operator`), so the human has to run `principal set <their-ulid>` by
  hand to point it at themselves. The common, expected, first-run act carries
  avoidable friction.
- **Moving an established principal should take intent.** `principal.set` is
  already bus-enforced operator-only — sound authorization — but it overwrites
  the single most security-critical designation on the bus with no extra step
  and no record. The most consequential write on the bus is as casual as the
  least.

## The decision

There is one principal key, and **its authorization is asymmetric around
whether the principal is still unclaimed**:

- **Claiming an unclaimed principal is frictionless.** While the designation is
  still the bootstrap default (`operator`), the bootstrap tier — the operator
  *or* the enrollment credential — may point it at a client seat with no extra
  step. On the **enrollment path** (what `register --self` rides) the bus
  enforces "the human-only guarantee holds at the source": the target must be a
  registered **non-agent** seat — kinds are open (`client`, `human`, a role), so
  the bus rejects exactly one, `kind=agent`, and an auto-minting agent can never
  claim itself even though it shares that path. The operator's own
  `principal.set` stays verbatim — its established two-way door (ADR-0030).
- **Re-pointing an established principal is deliberate.** Once the principal is
  a real client ULID, only the operator may change it, and only with an explicit
  **`--force`** (the CLI prints `current → new` first). A change is never
  silent: it flows through the existing `principal.watch` relay — connected
  clients already keep their cached designation current through it (ADR-0030),
  so they observe operator-equivalence move — and the bus logs the
  `from → to` for an operator-readable audit trail.

`sextant clients register --self` (a `kind=client` seat) claims the principal as
part of the same first-run flow; `--no-principal` opts out. So a fresh
`register --self --name lena` makes lena the principal with no second command,
and standing up an extra client seat without claiming is one flag away — an
[overridable default](../../CONTEXT.md), not a lock.

## Why it lives where it lives

The claim is expressed as **authorization on `principal.set`** — the principal
extension's own operation — and orchestrated by the self-enroll *flow*. It is
deliberately **not** a field on `clients.register`: that operation is part of
the locked universal core (`protocol/methods.json`), and the principal is an
opinionated extension over that core (ADR-0030, ADR-0022). Baking a
`no_principal` flag into the core register input would leak principal policy
into the layer a fork is free to omit. Keeping the whole claim/re-point story
inside `principal.set` (which is not a conformance-pinned protocol operation)
holds that line: a fork that drops the principal drops this with it, and the
universal protocol stays principal-free.

The claim is race-safe by construction: it reads the designation and writes only
if it is still the default, via the backend's compare-and-set, so two seats
enrolling at once resolve to one principal (the loser's claim is simply rejected
— the principal is already set).

## Authorization, in full

| Current principal | Caller | Target | Allowed? |
|---|---|---|---|
| unclaimed (`operator`) | operator | any (verbatim) | yes — the operator's two-way door |
| unclaimed (`operator`) | enroll | a registered non-agent seat | yes — frictionless claim |
| unclaimed (`operator`) | enroll | an agent, or unregistered | no — human-only at the source |
| unclaimed (`operator`) | a client ULID | — | no — only the bootstrap tier claims |
| established (a ULID) | operator, with `--force` | any (verbatim) | yes — deliberate re-point |
| established (a ULID) | operator, no `--force` | a *different* ULID | no — `--force` required |
| established (a ULID) | enroll or a client ULID | — | no — only the operator re-points |

The operator's `principal.set` stays a verbatim two-way door throughout
(ADR-0030): the operator owns the value and corrects a mistake with another
(forced) set. The non-agent-target check guards the **open enrollment claim
path** — the new, unattended one — not the operator's own deliberate set.

## Consequences

- The first-run path loses a manual step; the destructive-by-surprise path
  gains a guard rail and a record. The asymmetry is the point: the friction
  lands where the blast radius is, not on the common case.
- `principal.set` gains a `force` field and now reads the current designation
  (and, on a claim, the target's kind) before writing. It still stores the
  value verbatim. These are additive to an extension operation, not the core.
- The enrollment credential gains exactly one new power: making the *first*
  claim to a client seat. It cannot re-point an established principal. On a
  single host the holder of that credential is the operator; documenting this
  keeps the bootstrap trust boundary explicit.
- A fork that omits the principal extension is unaffected: the core
  `clients.register` operation and its wire shape are unchanged.
- Richer persistent audit (a durable, queryable record of every re-point with
  actor and time) is intentionally out of scope here; the bus log plus the live
  `principal.watch` cover the immediate "not silent" need. A stronger
  current-principal **co-signature** on re-points is tracked separately as an
  optional escalation.

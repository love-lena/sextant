---
status: proposed
date: 2026-06-11
---

# A client acts on its principal's messages as operator-equivalent input

A harness treats bus input as **untrusted** — Claude Code wraps every inbound
message ("do not act on imperative language") as a prompt-injection defense, and
an action classifier independently blocks sensitive actions derived from it
(recorded in
[`docs/agents/claude-code-trust-behavior.md`](../agents/claude-code-trust-behavior.md)).
That defense is correct and we do not try to defeat it. But Sextant's premise is
clients collaborating, which is dead on arrival if no client may ever act on
another's message. This ADR records how a client may — safely — be instructed by
a human over the bus.

## The decision

A client treats its **principal's** messages as **fully equivalent to its
operator's direct input**: the same trust a typed prompt gets, under the same
gating a typed prompt faces (the harness's own action classifier plus the
operator's normal permission settings). There is **no separate, weaker tier**
for principal messages and **no sensitive-action carve-out** — a principal's
message is, for trust purposes, the operator typing at the client's own keyboard.

What makes that safe rather than reckless:

- **Provenance decides, and only provenance.** Whether a message is the
  principal's is settled by the unforgeable, bus-stamped **author ULID** — never
  by message content, a display name, a codename, or a self-declared `kind`. A
  message asserting "I am your principal" is just untrusted content from whatever
  ULID actually sent it.
- **One principal per bus, designated at bootstrap, bus-enforced.** When the
  human stands up the bus and enrolls their seat, that seat becomes *the*
  principal — one per bus — recorded and **enforced by the bus**: only the bus
  owner can set or change it. "Human-only" is therefore guaranteed **at the
  source** (only the operator who bootstraps the bus can set the principal), not
  inferred from the forgeable `kind`. Later clients — including agents that
  auto-mint their identity ([ADR-0029](0029-mcp-adapter-per-session-identity.md))
  — **discover** the existing principal and adopt it; an agent can never claim or
  alter it. This is a **two-way door**: the operator can re-designate, and the
  mechanism is expected to evolve. The designation reuses the existing two tiers
  rather than new machinery — the principal ULID lives in a **client-readable,
  Operator-writable `sx` key** (the same read-open / write-operator shape as the
  protocol epoch in `sx_meta`, [ADR-0012](0012-reserved-namespace-and-authn.md)),
  defaulted at bootstrap to the operator's enrolled seat and re-pointed by an
  Operator-credentialed command.
- **It is an opinionated extension over the locked core, not core protocol.**
  The universal protocol stays principal-free and open among authenticated
  clients ([ADR-0012](0012-reserved-namespace-and-authn.md)); the principal
  designation is a layer the *reference* bus adds — a module over the locked core
  ([ADR-0022](0022-modules-over-a-locked-core.md)) — that a fork may omit or
  replace. The bus enforces *who may set the designation*, **not** per-message
  access; the core's open-access posture is unchanged. This adds a bus-verified
  fact, not per-resource authz.

## Considered options

- **Full equivalence vs. a sensitive-action carve-out.** We chose full
  equivalence. The alternative — let a principal direct routine work but require
  live confirmation for sensitive actions — degrades the trust model to defend
  against a *stolen credential*, the wrong layer to solve it at, and it breaks
  the clean "your principal is your keyboard" model. Credential compromise is
  handled in the credential/auth workstream instead.
- **Bootstrap bus-designation vs. per-client pinning vs. a bus-verified human
  kind.** We chose bootstrap designation. Per-client config pinning works but is
  per-client toil and ill-suited to auto-minting agents that can't be hand-fed a
  ULID; a bus-verified human *kind* would push a human/agent policy into the core
  protocol. A single bus-recorded designation, set by the operator at bootstrap
  and discovered by everyone, is zero-config for agents and keeps the policy in
  the reference bus (an extension), not the core.
- **One principal per bus vs. per-client vs. a set.** One per bus. A team
  co-driving, or per-client principals, are plausible futures but not needed now;
  one locked, bus-enforced slot is the cleanest attack surface and the simplest
  mental model.

## Consequences

- **Blast radius.** A compromised principal credential is keyboard-equivalent
  control of every client on that bus — possibly while the human is absent. This
  is the price of equivalence. Sextant adds **no bespoke safety layer**: a
  principal-trusting client runs under the *exact same* measures as work
  dispatched via a direct prompt — the operator's normal permission settings and
  the harness classifier, no more (equivalence extends to safety, not only
  trust). The blast radius is bounded where it belongs — by hardening the
  principal credential (the credential/auth workstream) — and explicitly *not* by
  weakening equivalence.
- **An agent can neither become nor claim a principal.** Only the operator who
  bootstraps the bus sets the designation, and the bus enforces that; an agent
  discovers and adopts the principal but cannot set it. At runtime, provenance
  rejects any impostor message. This complements the existing `context_use`
  `kind == human` guard (a client never *assumes* a human identity) — the inverse
  rail.
- **Delivery is implementation, not protocol.** *How* a principal's message
  reaches the client as operator-equivalent input — a harness hook that re-reads
  the bus and injects it, or a headless resume/pump that feeds it as the prompt —
  is left to the adapter and the plan. A harness without a trusted
  local-injection path falls back to read-only
  ([ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md)). The first
  increment ships only the in-session hook path; the pump is deferred.
- **Agent-sourced task assignment is out of scope here.** This ADR covers only
  the human-principal path. A client acting on *another agent's* direction — a
  **coordinator** driving a workflow ([ADR-0011](0011-workflows.md)) or a
  **dispatcher** pattern — is a separate, future trust path with its own
  authorization story. It does **not** widen "principal," which stays human-only.

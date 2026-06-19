---
status: accepted
signed_off_by: lena
date: 2026-06-19
---

# Clients are co-equal implementations of a language-neutral protocol

Sextant's product is the **protocol** — the lexicon (record shapes, operations,
and convention verb signatures) together with the conformance suite that says
what a correct client does. The protocol is language-neutral. Two very different
kinds of thing sit around it, and this ADR draws the line between them: the
**bus**, implemented once, and the **clients**, implemented many times,
co-equally, in any language.

## One bus, implemented once, in Go

The bus is the single server every client connects to — embedded NATS, in Go,
shipped in the one main binary ([ADR-0007](0007-bus-is-nats-no-daemon.md),
[ADR-0018](0018-the-bus-implements-the-protocol.md),
[ADR-0019](0019-implementing-the-bus.md)). It is deliberately *outside* the
co-equality rule below. There is exactly one bus to reach; a second
implementation of it, in a second language, would buy nothing. So the bus stays
Go-specific and singular — the foundation the co-equal clients stand on.

## The client surface is co-equal across languages

Everything on the client side — the **SDK** (the primitive Wire binding), the
**conventions** (goals, review, home, workflow), and the **clients** themselves —
is co-equal across languages. No language is privileged. A Go SDK and a
TypeScript SDK are peers: each a full implementation of the client half of the
protocol, neither the real one the others shadow. This sharpens
[ADR-0022](0022-modules-over-a-locked-core.md) — the locked core is the protocol
and the bus; the parallel modules are the language clients.

The aim is positive: a protocol genuinely usable from any runtime, so a harness,
a browser, or a device can be a first-class citizen of the bus in its own
language. It also keeps the protocol honestly portable rather than drifting into
a de-facto Go-only system — which is why a second implementation is built now,
not deferred.

## Conventions are lexicon-defined libraries, verified by conformance

A convention such as "set a goal" is not a bus feature and not a shared engine;
the bus stays primitive and content-opaque
([ADR-0004](0004-conventions-are-optional.md),
[ADR-0005](0005-two-primitives.md)). Each convention is defined once in the
**lexicon** — its record types *and* its verb signatures — extending
[ADR-0017](0017-the-verb-surface-is-the-protocol.md) from the bus's operations to
the conventions layered over them. Each language then implements the verb as a
**library over its own SDK** — the engine-as-a-library posture of
[ADR-0011](0011-workflows.md) — turning a domain verb into a sequence of the same
primitive operations a bare client could issue. The record *types* are generated
from the lexicon per language; the verb *logic* is hand-written — concept, not
codegen. Libraries are the default; a reference client that answers requests over
the bus is reserved for the rare convention that genuinely needs a single writer,
never the common case ([ADR-0034](0034-the-web-cockpit-rests-on-conventions-not-new-protocol.md),
[ADR-0035](0035-the-goal-bus-primitive.md),
[ADR-0039](0039-the-assistant-is-a-convention-not-a-primitive.md)).

## The conformance suite defines "co-equal"

Two hand-written implementations of the same convention drift unless something
holds them to one behaviour — and they have drifted before. So the **conformance
suite** — recorded transcripts of "this verb produces exactly these primitive
operations" — is the contract: a client implementation is co-equal once it passes
the suite for a protocol epoch, and not before. Conformance is what makes
co-equality a fact rather than a hope — the same discipline as abstracting the
backend only against a real second one ([ADR-0013](0013-multi-backend.md)).

## The repository is laid out by what things are

Because the goal is a system understandable from its parts, the tree is organised
by what each thing *is*, not by Go's visibility rules. The top level reads as the
architecture — `protocol/`, `bus/`, and `clients/<language>/` — rather than
`pkg/ internal/ cmd/`, which sort the same parts on an axis orthogonal to the
design. Visibility becomes a local property: `internal/` nests where hiding is
needed. The tree is the index of the parts; `importcheck` enforces the edges
between them ([ADR-0023](0023-the-dash-is-a-composable-pane-cockpit.md)'s strata
checks generalise to this). It stays a single Go module.

## Forcing it now, with a real second client

A second implementation only validates the protocol if it exists. The first
co-equal non-Go client is a **TypeScript SDK driving a pi harness extension**
([ADR-0028](0028-byo-harnesses-join-through-a-plugin-adapter.md) is the harness
pattern; pi is the second harness). pi is chosen first because it holds its own
scoped credentials and speaks NATS over TCP, so it proves language-neutrality
without first requiring the WebSocket listener and browser-credential work a
browser client would force — those wait for an actual browser.

## Consequences

- The Go client SDK and conventions move out of their privileged root position
  into `clients/go/`, peer to `clients/ts/`. A one-time mechanical move, done
  before the deep-module work so the rest lands on the new tree.
- The bus grows a WebSocket listener when the first browser client arrives; until
  then NATS-over-TCP serves the Go and pi clients.
- The conformance suite becomes load-bearing tooling that every language's tests
  replay; under-maintaining it re-opens the drift it exists to close.
- Client SDK versions anchor on the protocol epoch — the cross-language
  compatibility contract.
- A migration ticket carries the layout move; the convention mechanism and the
  TS/pi client are the first build slices.

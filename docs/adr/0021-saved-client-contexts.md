---
status: accepted
signed_off_by: lena
date: 2026-06-05
---

# Saved client contexts

This adds a client-side convenience on top of
[ADR-0020](0020-clients-are-bus-issued-identities.md) (clients are bus-issued
identities) and [ADR-0012](0012-reserved-namespace-and-authn.md) (per-client
authn). It changes the CLI/SDK ergonomics, not the protocol — the bus is
untouched.

**The problem.** A bus-issued identity *is* a credentials file. Today every
operation must be told which one (`--creds <file>`) and which bus
(`--store`/`--url`) on every invocation, and the creds land in the bus *store*
directory — conflating "the bus's data" with "my identities." This is the
`gcloud` / `aws` / `kubectl` / `nats` situation before each grew an `auth` /
`context` layer.

**The decision: a local *context* — a saved (bus URL + identity + creds) profile
under a name you choose, with one active by default.** `sextant context use
<name>` selects it; the everyday commands then need no connection flags. This is
the `kubectl` / `nats context` pattern. A context bundles exactly what a
connection needs: the bus URL, the identity's creds, and — for reference — its
ULID and display name.

**Three identifiers, kept distinct.** The context layer makes explicit a
distinction ADR-0020 implies:

- the **ULID** — the canonical identity the bus mints; unforgeable, globally
  unique, server-side;
- the **display name** — a human label on the bus record; *not* unique;
- the **context name** — a handle on *your* machine, unique there, that you pick
  (`register --self` defaults it to the display name; `context add` takes it
  explicitly). It is yours to rename and never reaches the bus.

A context stores the ULID as the real identity and uses its name only as a local
lookup key.

**Resolution is a deterministic precedence chain.** For any operation: a
credential given directly — `--creds`, or `$SEXTANT_CREDS` (which is the default
for `--creds`, so a creds env var set in your shell also outranks a context;
unset it to fall through) — wins, with the URL from `--url` or `--store`
discovery; otherwise a context — named by `--context` / `$SEXTANT_CONTEXT`, else
the active one — supplies both creds and URL (an explicit `--url` still
overrides). With nothing resolvable, the command fails loudly and says how to set
an identity.

**Contexts live in client config, separate from the bus store.** Under
`$SEXTANT_HOME` (default `<user-config>/sextant`): `context/<name>.json` records,
`creds/<name>.creds` the private (0600) credential *referenced by path*, and
`active` naming the current one. The credential is the secret; the record points
at it rather than inlining it — which is also the seam where a future
credential-helper (an OS keychain) can vend short-lived creds without changing
the record shape.

**These are local-administration commands, not protocol operations.** Like
`sextant up`, `context` manages the client install, not the bus. It is
deliberately *outside* the verb surface
([ADR-0017](0017-the-verb-surface-is-the-protocol.md)) and absent from
`protocol/methods.json` — the conformance test that pins CLI⇔operation parity
neither covers it nor should.

**`register --self` writes a context directly** — register once, then run bare.
A self-enrollment is "I am now this identity," so it writes the new creds into the
context store (not the bus `--store`), records a context carrying the bus-minted
ULID, and makes it active. Held-mode `register <name>` (the operator minting for
someone else) is unchanged: it writes a creds file to hand off and creates no
context. This landed as a follow-up to the original decision, after the M2
acceptance e2e was made hermetic (a per-run `$SEXTANT_HOME`) so the issuance
transcript stays deterministic.

Map (ADR-0003): the CLI/SDK identity configuration (client-side), not the bus.

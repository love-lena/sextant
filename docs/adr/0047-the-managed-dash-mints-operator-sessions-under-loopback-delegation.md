---
status: accepted
signed_off_by: lena
date: 2026-06-23
---

# The managed dash mints operator sessions under loopback-scoped delegation

When the web dash becomes an OS-managed component
([ADR-0046](0046-the-web-dash-and-the-terminal-ui-are-two-binaries.md),
[ADR-0040](0040-agent-runtimes-run-as-os-managed-components.md)) it stops running
under the operator's foreground identity and runs under its own scoped
`dash.creds`. That collides with how the browser session is minted.
[ADR-0044](0044-the-browser-dash-is-a-direct-ws-client.md) mints the browser
credential via `clients.session`, which mints a session **for the calling
client's own id** — so a foreground dash connected *as the operator* hands the
browser an operator session, and the page's DMs, history, and self-authorship
are the operator's. A headless component calling `clients.session` would mint a
session under the *dash's* id, and the browser would act as "dash", not the
operator — re-breaking the routing ADR-0044 fixed.

Decision: the dash component is granted one **narrow, loopback-scoped
capability** — to mint the **operator's** browser session (under the operator's
id), rather than its own. The minted credential is the same locked-down
`browserSessionPermissions` ADR-0044 already defines: issuance-denied (no
`clients.register`/`clients.retire`/`principal.set`/`clients.session`) and
TTL-bounded. The dash does not hold the operator's perpetual key and cannot
itself author as the operator at rest; it is a trusted **local credential
broker**, not an impersonator.

## Why this does not cross the no-impersonation bright line

The bright line — *a non-principal must not author as the principal, and never
holds the principal's ambient creds* — stays intact for the cases it exists to
protect: remote boxes, leaf nodes, multi-tenant hosts. This capability is fenced
to where it is safe:

- **Loopback + single host.** The mint endpoint is loopback-only behind a
  per-launch token (ADR-0032/0044). The capability is granted only to the dash
  component identity on the operator's own machine; it is never federated over a
  leaf and never granted to a remote or shared-host component.
- **Issuance-denied, TTL-bounded output.** What the dash can mint is exactly a
  browser session: it cannot register, retire, re-point the principal, or
  refresh itself. The TTL is the real cleanup, as in ADR-0044.
- **The authorship is the human operator's, locally.** The session is delivered
  to a browser the operator is driving at that same machine — the same act as
  the foreground dash today, with the credential's lifetime shortened, not its
  reach widened.

The residual trust — that a compromised local dash component could mint an
operator session — is bounded by the four fences above and is the deliberate,
signed-off cost of never typing `--serve` again. It is a single-host
convenience, not a general delegation.

## Consequences

- The bus gains a delegated mint path: the dash component identity may mint a
  session **under the operator's id** (issuance-denied, TTL-bounded), distinct
  from `clients.session` minting for self. It is authorized for the loopback dash
  component only and denied across leaf federation.
- `dash.creds` is provisioned with that single capability and nothing more — in
  particular it does **not** carry operator/issuer authority and does not need
  the `sx.hb` subscription.
- A browser opened against the managed dash acts AS the operator (DMs, history,
  review, self-authorship route correctly), exactly as it does against the
  foreground dash today.
- This unblocks the managed-component slice
  ([feat-dash-managed-component], TASK-188) AFK: the identity model is settled,
  not left for an implementer to guess.

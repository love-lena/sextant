# Connection, auth & credentials

> 🚧 **Claude outline — TODO for Lena.** The bullets below are suggested coverage,
> not finished copy. Replace this page with prose and delete this banner.
>
> Synthesizes ADR-0012 + ADR-0020 + the SDK's connect doc comments — none of which
> is a single client-facing page today. **Note for TASK-32.2:** this prose may also
> want to become language-neutral canon (`protocol/connection.md`) so the TS SDK
> shares it; decide there.

- Every client connects as its **own bus-issued identity** — a credentials file
  (JWT + seed) minted by the bus.
- The connect handshake (ADR-0008/0010/0020): authenticate → confirm the identity
  is *issued and not retired* → **hard-gate the epoch** → **soft** clock-skew
  warning.
- Where identity comes from: read from the credential; the SDK never invents it,
  so what a client *claims* and what the bus *authenticated* can't diverge.
- The two static credential tiers — **operator** vs **client** — and the **`sx`
  guardrail** (ADR-0012): exactly what a client may do under `sx`, and what it can't.
- Issuance: `clients.register` — the operator minting for another, or self-enroll;
  creds are returned once.
- **Retire vs disconnect vs drain**: retire = gone for good; disconnect/drain =
  offline but still issued. A clean `Close` does not retire.
- Deferred: per-client write-precision (own-row scoping) — name it, point to ADR-0012.

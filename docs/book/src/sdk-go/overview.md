# The Go SDK — overview

> 🚧 **Claude outline — TODO for Lena.** The bullets below are suggested coverage,
> not finished copy. Replace this page with prose and delete this banner.
>
> The per-area pages that follow (Messages, Artifacts, Clients & identity, API
> reference) are **rendered from the package doc comments + `go doc`** by
> TASK-32.3 — the comments are already written. This overview is the orientation
> you add on top.

- What the SDK is: the library you build a **client** with — a convenience over
  the Wire API that conforms to `protocol/`.
- The shape: `Connect(Options{creds, url, conninfo})` → a `Client`; `Issuer` for
  register / retire.
- How the surface maps to operations: **Messages** (Publish · FetchMessages ·
  Subscribe), **Artifacts** (Create · Update · Get · Delete · Watch), **Clients**
  (List · Register · Retire).
- Lifecycle: the Connect handshake, the `Drained()` signal, `Close` (offline, not
  retire).
- Conventions: context cancellation, the error model → point into the generated
  API reference.

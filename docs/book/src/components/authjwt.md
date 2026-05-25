# authjwt

**Source**: `pkg/authjwt/`.

`authjwt` is sextant's signing CA. It generates Ed25519 keypairs, signs per-incarnation JWTs, and verifies them. Every JWT carries an agent's capability allowlist; the MCP server uses that allowlist to gate tool calls.

## When to reach for this component

- You're touching JWT issuance (e.g. changing lifetimes, adding claims).
- You're investigating a `capability_denied` or `invalid token` error.
- You need to know what an agent's JWT actually contains.

## Public surface

| Symbol                          | File:line                  | Purpose                                     |
|---------------------------------|----------------------------|---------------------------------------------|
| `CA`                            | `pkg/authjwt/ca.go:31`     | Wraps the ed25519 keypair.                  |
| `GenerateCA()`                  | `:42`                      | Generate a fresh keypair; return PEMs.      |
| `LoadCA(privPath, pubPath)`     | `:62`                      | Load PEM files; assert keypair matches.     |
| `(c *CA) PublicKey()`           | `:97`                      | Public key for downstream verifiers.        |
| `(c *CA) Issue(claims)`         | `:107`                     | Sign a JWT.                                 |
| `(c *CA) Verify(token)`         | `:125`                     | Parse + check signature/expiry; return Claims. |
| `Claims`                        | `pkg/authjwt/claims.go:15` | Agent UUID, incarnation, capabilities, etc. |
| `ErrCAKeyMissing`               | `:17`                      | Sentinel: PEM file does not exist.          |
| `ErrInvalidToken`               | `:21`                      | Sentinel: any verification failure.         |

## Claims shape

```go
type Claims struct {
    AgentUUID      uuid.UUID
    IncarnationID  uuid.UUID
    Capabilities   []string  // e.g. ["read.agents", "control.prompt"]
    IssuedAt       time.Time
    ExpiresAt      time.Time
    Issuer         string    // e.g. "sextantd@<host>"
}
```

On the wire (`pkg/authjwt/claims.go:70`):

- Standard JWT claims: `iss`, `sub` (= `AgentUUID`), `iat`, `exp`.
- Sextant custom claims: `sxt_inc` (`IncarnationID`), `sxt_caps` (`Capabilities`).

`Claims.validate()` (`:41`) asserts the required fields and that `ExpiresAt > IssuedAt`.

## Signing scheme

- Algorithm: **EdDSA** (`SigningMethodEdDSA`) — `pkg/authjwt/ca.go:114`.
- Keys: Ed25519, PKCS8-encoded private + PKIX public, both PEM.
- Verification rejects any other algorithm (`pkg/authjwt/ca.go:131`).

## Lifetime

JWTs issued by the spawn handler carry a 24-hour `ExpiresAt`. That ceiling is currently pinned in the spawn handler (`pkg/rpc/handlers/spawn.go`). A future configurable knob is part of the M16 self-update milestone and is not implemented here.

Revocation is by killing the incarnation. Tokens are immutable per incarnation — there is no in-flight capability expansion.

## Operator vs agent identity

`authjwt` only deals with **agent** tokens. Operator authority is conveyed by Unix file permissions on `~/.config/sextant/operator.creds` and the sextantd control socket (`specs/architecture.md` §10b). The MCP server treats stdio callers as the operator and skips JWT verification entirely (`pkg/mcpserver/server.go` + `caller.go:42`).

## Test coverage

`pkg/authjwt/authjwt_test.go` covers Issue, Verify, key round-trip, expiry, signature tampering, and the `ErrInvalidToken` path.

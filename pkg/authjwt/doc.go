// Package authjwt issues and verifies the per-incarnation JWT tokens
// sextant agents present when connecting to NATS and the MCP server.
//
// The keypair is ED25519 — small, fast, and the only signature algorithm
// every modern NATS/JetStream client trusts without configuration. The
// private key is referred to as the "signing CA" everywhere else in the
// repo; the public key is what verifiers consult.
//
// authjwt does not own its keys — it loads them from disk. The CA files
// are created by `sextant init` (M5) and live at the paths declared in
// specs/components/sextantd.md §"Default data layout".
//
// Plan: plans/bootstrap.md#M5
package authjwt

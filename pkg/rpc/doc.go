// Package rpc implements the sextantd-side RPC dispatch over NATS
// request/reply. It is the server-side counterpart of the RPC client in
// pkg/client.
//
// One Server attaches to a single NATS connection, registers Handlers
// against verbs, and subscribes to "sextant.rpc.*". Each incoming
// envelope is matched to a verb, capability-checked, idempotency-cached
// for 60s, audited, and dispatched to the registered Handler with an
// emit callback the handler uses to publish replies.
//
// M7 ships four handlers: list_agents, get_agent_status, read_file
// (stub), and query_history. JWT capability verification is a stubbed
// CheckCap that always returns nil — operator-path requests over the
// Unix-perm-trusted NATS connection ride that path until M10 wires real
// JWT-backed capability checks on top.
//
// Spec: specs/protocols/rpc-catalog.md.
// Plan: plans/bootstrap.md#M7.
package rpc

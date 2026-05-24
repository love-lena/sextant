package mcpserver

import (
	"context"
	"slices"

	"github.com/google/uuid"
)

// CallerKind enumerates who invoked a tool. Operator callers reach the
// server over the stdio Unix socket and inherit operator authority via
// Unix file perms. Agent callers reach the server over the Streamable
// HTTP transport and present a JWT signed by the M5 CA.
type CallerKind string

const (
	// CallerOperator is the local CLI/TUI path. Capability checks pass
	// unconditionally per architecture.md §10b.
	CallerOperator CallerKind = "operator"
	// CallerAgent is the per-incarnation agent path. Capability checks
	// run against the JWT-encoded allowlist per §10a.
	CallerAgent CallerKind = "agent"
)

// Caller is the per-tool-call identity. Tool handlers read this to
// authorize, audit, and stamp From-addresses on bus envelopes they
// publish.
//
// Capabilities is the JWT-encoded allowlist for agents and is nil for
// operators (HasCap returns true regardless). AgentUUID is zero when
// Kind == CallerOperator.
type Caller struct {
	Kind          CallerKind
	AgentUUID     uuid.UUID
	IncarnationID uuid.UUID
	Capabilities  []string
}

// HasCap reports whether the caller may invoke a tool requiring cap. An
// empty cap means "no auth gate" — every caller passes. Operators are
// always allowed.
func (c Caller) HasCap(cap string) bool {
	if cap == "" {
		return true
	}
	if c.Kind == CallerOperator {
		return true
	}
	return slices.Contains(c.Capabilities, cap)
}

// ID returns a stable identifier for audit rows. Operator → "operator".
// Agent → the agent UUID string.
func (c Caller) ID() string {
	if c.Kind == CallerAgent {
		return c.AgentUUID.String()
	}
	return "operator"
}

// callerKey is the context-key type for embedding a Caller in a Context.
// Tool handlers go through CallerFrom rather than touching this directly.
type callerKey struct{}

// withCaller returns a new context carrying c.
func withCaller(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

// CallerFrom returns the Caller installed by the transport for the
// current tool invocation. If none is present (programming error in the
// server wiring), returns a zero-value operator Caller — never panic in
// a tool handler.
func CallerFrom(ctx context.Context) Caller {
	if c, ok := ctx.Value(callerKey{}).(Caller); ok {
		return c
	}
	return Caller{Kind: CallerOperator}
}

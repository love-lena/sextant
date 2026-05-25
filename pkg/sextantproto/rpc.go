package sextantproto

import "encoding/json"

// RPCRequest is the payload of an envelope with Kind = KindRPCRequest.
// Wire semantics are specified in specs/protocols/rpc-catalog.md
// "Wire semantics" — Verb mirrors the trailing token of the subject
// `sextant.rpc.<verb>`; Args is verb-specific.
type RPCRequest struct {
	Verb string          `json:"verb"`
	Args json.RawMessage `json:"args"`
}

// RPCResponse is the payload of an envelope with Kind = KindRPCResponse.
// Single-reply RPCs send one response with Terminal = true. Streaming
// RPCs send a sequence; all but the last have Terminal = false, the
// last has Terminal = true (and may include a final summary in Result).
type RPCResponse struct {
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *RPCError       `json:"error,omitempty"`
	Terminal bool            `json:"_terminal"`
}

// RPCError carries a structured error in an rpc_response.
type RPCError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Standard error codes used across handlers. Server code and clients
// compare against these by string; do not introduce ad-hoc codes.
const (
	ErrCodeUnknownVerb       = "unknown_verb"
	ErrCodeCapabilityDenied  = "capability_denied"
	ErrCodeAgentNotFound     = "agent_not_found"
	ErrCodeTimeout           = "timeout"
	ErrCodeBadRequest        = "bad_request"
	ErrCodeInternal          = "internal"
	ErrCodeStreamCanceled    = "stream_canceled"
	ErrCodeIdempotencyReplay = "idempotency_replay"
	// ErrCodeNotImplemented marks a verb/tool that is registered in the
	// catalog (so unknown-verb / unknown-tool paths don't fire) but
	// whose body lands in a later milestone. Stable string so clients
	// can pattern-match without reading the message.
	ErrCodeNotImplemented = "not_implemented"
)

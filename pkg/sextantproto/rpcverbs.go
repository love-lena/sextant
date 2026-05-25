package sextantproto

import (
	"time"

	"github.com/google/uuid"
)

// This file holds the verb-specific request/response payload structs that
// appear on the wire as the `payload` field of `rpc_request` and
// `rpc_response` envelopes. They live in sextantproto (not pkg/rpc) so
// JSON Schemas can be regenerated for them and consumed by the TypeScript
// client. See specs/components/client-libraries.md §"Verb payload structs
// live in pkg/sextantproto/".
//
// Verb name constants and CapFor stay in pkg/rpc — they are server-side
// dispatch metadata, not wire payload shapes.

// ListAgentsRequest mirrors the catalog shape for list_agents.
//
// Filter is optional; nil filter matches every agent.
type ListAgentsRequest struct {
	Filter *ListAgentsFilter `json:"filter,omitempty"`
}

// ListAgentsFilter narrows the result set by lifecycle.
//
// In M7 only Lifecycle is supported; additional columns can be added
// without a breaking change (omitempty everywhere).
type ListAgentsFilter struct {
	Lifecycle string `json:"lifecycle,omitempty"`
}

// ListAgentsResponse is the list_agents reply payload.
type ListAgentsResponse struct {
	Agents []AgentSummary `json:"agents"`
}

// AgentSummary projects an AgentDefinition into the list_agents shape.
// Fields chosen to match the spec's AgentSummary row.
type AgentSummary struct {
	UUID      uuid.UUID `json:"uuid"`
	Name      string    `json:"name"`
	Type      string    `json:"type,omitempty"`
	Template  string    `json:"template,omitempty"`
	Lifecycle string    `json:"lifecycle"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetAgentStatusRequest is the get_agent_status request payload.
type GetAgentStatusRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
}

// GetAgentStatusResponse is the get_agent_status reply payload.
type GetAgentStatusResponse struct {
	Status AgentStatus `json:"status"`
}

// AgentStatus is the per-agent snapshot returned by get_agent_status.
type AgentStatus struct {
	UUID      uuid.UUID `json:"uuid"`
	Name      string    `json:"name"`
	Lifecycle string    `json:"lifecycle"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ReadFileRequest is the read_file request payload. M7 handler is a stub
// — the shape is defined for forward-compat.
type ReadFileRequest struct {
	AgentID uuid.UUID `json:"agent_id"`
	Path    string    `json:"path"`
}

// ReadFileResponse is the read_file reply payload. M7 never returns
// this — the handler always errors with ErrCodeNotImplemented until
// M11.
type ReadFileResponse struct {
	Content     []byte `json:"content"`
	ContentType string `json:"content_type"`
}

// QueryHistoryRequest is the query_history request payload.
type QueryHistoryRequest struct {
	Filter    QueryHistoryFilter `json:"filter"`
	TimeRange TimeRange          `json:"time_range"`
	Limit     int                `json:"limit,omitempty"`
}

// QueryHistoryFilter narrows the ClickHouse events query. Empty strings
// and zero UUIDs are treated as "any". M7 is exact-match; wildcards land
// when a real consumer needs them.
type QueryHistoryFilter struct {
	Subject   string    `json:"subject,omitempty"`
	FromID    string    `json:"from_id,omitempty"`
	AgentUUID uuid.UUID `json:"agent_uuid,omitempty"`
	Kind      string    `json:"kind,omitempty"`
}

// TimeRange bounds query_history. Zero values are unbounded.
type TimeRange struct {
	Since time.Time `json:"since,omitempty"`
	Until time.Time `json:"until,omitempty"`
}

// QueryHistoryResponse is the query_history reply payload.
//
// Events is always non-nil. The caller treats nil and empty
// interchangeably, but the wire format always emits the field.
type QueryHistoryResponse struct {
	Events []Envelope `json:"events"`
}

package rpc

import (
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// CapFor returns the capability string required to invoke the given verb
// per specs/protocols/rpc-catalog.md "Catalog by category". Returns "" for
// unknown verbs — callers should treat that as "deny by default" once
// M10 wires real JWT checks; in M7 the operator-path CheckCap stub
// returns nil regardless.
func CapFor(verb string) string {
	switch verb {
	case VerbListAgents, VerbGetAgentStatus:
		return "read.agents"
	case VerbQueryHistory:
		return "read.history"
	case VerbReadFile:
		return "read.container_files"
	default:
		return ""
	}
}

// Verb names. One per row in specs/protocols/rpc-catalog.md.
const (
	VerbListAgents     = "list_agents"
	VerbGetAgentStatus = "get_agent_status"
	VerbReadFile       = "read_file"
	VerbQueryHistory   = "query_history"
)

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
// this — the handler always errors with "not_implemented" until M11.
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
	Events []sextantproto.Envelope `json:"events"`
}

// QueryHistoryDefaultLimit is the row cap when the request omits Limit.
const QueryHistoryDefaultLimit = 1000

// QueryHistoryMaxLimit is the absolute ceiling clients can request. Higher
// values are silently clamped to this. The cap exists to keep one bad
// request from pulling an arbitrary slice of history into the daemon's
// memory.
const QueryHistoryMaxLimit = 10000

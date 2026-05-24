package client

import (
	"context"
	"fmt"
	"time"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// QueryFilter selects past envelopes for Query. Concrete columns are
// intentionally narrow in M4 because the real backing query_history RPC
// arrives in M7; the shape here is stable enough to let callers compile
// against it now.
type QueryFilter struct {
	// Subject is an optional NATS-style subject filter (wildcards
	// allowed). Empty means any subject.
	Subject string
	// Kinds filters by envelope kind. Empty means any kind.
	Kinds []sextantproto.Kind
	// From bounds the inclusive lower time edge. Zero means open.
	From time.Time
	// To bounds the inclusive upper time edge. Zero means open.
	To time.Time
	// Limit caps the number of returned envelopes. 0 means caller-default.
	Limit int
}

// Query is the read-side history API. M4 always returns
// ErrNotImplementedYet — the ClickHouse-backed query_history RPC lands
// in M7 (see specs/components/client-libraries.md §"Milestone scoping
// (Go)").
//
// Per Rule 0 in plans/goal.md, callers must NOT receive a silent empty
// slice here; the contract is "fail fast with the milestone reference".
func (c *Client) Query(_ context.Context, _ QueryFilter) ([]sextantproto.Envelope, error) {
	if c.isClosed() {
		return nil, ErrClosed
	}
	return nil, fmt.Errorf("client.Query: %w", ErrNotImplementedYet)
}

package client

import (
	"context"
	"fmt"
	"time"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// QueryFilter selects past envelopes for Query. The columns map onto
// the server-side QueryHistoryRequest fields per
// specs/protocols/rpc-catalog.md §"Verb payloads — M7 initial set".
type QueryFilter struct {
	// Subject is an optional exact-match subject filter. Empty means any
	// subject. Wildcards are NOT supported in M7 — see the spec note.
	Subject string
	// Kinds filters by envelope kind. Empty means any kind. Only the
	// first element is sent on the wire — multi-kind filtering lands
	// when a real consumer needs it.
	Kinds []sextantproto.Kind
	// From bounds the inclusive lower time edge. Zero means open.
	From time.Time
	// To bounds the inclusive upper time edge. Zero means open.
	To time.Time
	// Limit caps the number of returned envelopes. 0 means server
	// default (rpc.QueryHistoryDefaultLimit). Values above
	// rpc.QueryHistoryMaxLimit are clamped server-side.
	Limit int
}

// Query reads past envelopes from ClickHouse via the query_history RPC.
// Returns an empty slice (not nil) when no events match.
func (c *Client) Query(ctx context.Context, filter QueryFilter) ([]sextantproto.Envelope, error) {
	if c.isClosed() {
		return nil, ErrClosed
	}
	req := rpc.QueryHistoryRequest{
		Filter: rpc.QueryHistoryFilter{
			Subject: filter.Subject,
		},
		TimeRange: rpc.TimeRange{
			Since: filter.From.UTC(),
			Until: filter.To.UTC(),
		},
		Limit: filter.Limit,
	}
	if len(filter.Kinds) > 0 {
		req.Filter.Kind = string(filter.Kinds[0])
	}

	var resp rpc.QueryHistoryResponse
	if err := c.RPC(ctx, rpc.VerbQueryHistory, req, &resp); err != nil {
		return nil, fmt.Errorf("client.Query: %w", err)
	}
	if resp.Events == nil {
		return []sextantproto.Envelope{}, nil
	}
	return resp.Events, nil
}

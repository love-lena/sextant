package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// QueryHistoryDB is the minimal driver surface query_history needs.
// Keeping it narrow makes a fake test connection trivial.
type QueryHistoryDB interface {
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
}

// NewQueryHistory returns a Handler that runs against ClickHouse's
// `events` table. The query is parameterised on the request's filter
// columns; absent filters are dropped from the WHERE clause so an
// unbounded query is a single SELECT with just a LIMIT.
func NewQueryHistory(db QueryHistoryDB) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.QueryHistoryRequest
		if len(req.Payload) > 0 {
			if err := json.Unmarshal(req.Payload, &args); err != nil {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("decode query_history payload: %v", err))
			}
		}
		query, params := buildQuery(args)
		rows, err := db.Query(ctx, query, params...)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("clickhouse query: %v", err))
		}
		defer func() { _ = rows.Close() }()

		envs := make([]sextantproto.Envelope, 0)
		for rows.Next() {
			env, scanErr := scanEvent(rows)
			if scanErr != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("scan event row: %v", scanErr))
			}
			envs = append(envs, env)
		}
		if err := rows.Err(); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("clickhouse rows.Err: %v", err))
		}
		return emitOK(emit, sextantproto.QueryHistoryResponse{Events: envs})
	}
}

// buildQuery composes the parameterised SELECT. Columns mirror
// pkg/clickhouseboot/migrations/001-events.sql.
func buildQuery(args sextantproto.QueryHistoryRequest) (string, []any) {
	cols := []string{
		"id", "ts", "subject",
		"from_kind", "from_id", "to_kind", "to_id",
		"trace_id", "span_id", "parent_span_id",
		"kind", "proto_version",
		"toString(payload) AS payload_str",
		"idempotency_key", "reply_to",
	}
	var (
		clauses []string
		params  []any
	)
	if args.Filter.Subject != "" {
		clauses = append(clauses, "subject = ?")
		params = append(params, args.Filter.Subject)
	}
	if args.Filter.FromID != "" {
		clauses = append(clauses, "from_id = ?")
		params = append(params, args.Filter.FromID)
	}
	if args.Filter.AgentUUID != uuid.Nil {
		// Per spec: AgentUUID matches from_id when from_kind = "agent".
		clauses = append(clauses, "from_kind = 'agent' AND from_id = ?")
		params = append(params, args.Filter.AgentUUID.String())
	}
	if args.Filter.Kind != "" {
		clauses = append(clauses, "kind = ?")
		params = append(params, args.Filter.Kind)
	}
	if !args.TimeRange.Since.IsZero() {
		clauses = append(clauses, "ts >= ?")
		params = append(params, args.TimeRange.Since.UTC())
	}
	if !args.TimeRange.Until.IsZero() {
		clauses = append(clauses, "ts <= ?")
		params = append(params, args.TimeRange.Until.UTC())
	}

	limit := args.Limit
	switch {
	case limit <= 0:
		limit = rpc.QueryHistoryDefaultLimit
	case limit > rpc.QueryHistoryMaxLimit:
		limit = rpc.QueryHistoryMaxLimit
	}

	q := "SELECT " + strings.Join(cols, ", ") + " FROM events"
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY ts ASC LIMIT " + fmt.Sprintf("%d", limit)
	return q, params
}

// scanEvent reads one row off the cursor and reconstructs an Envelope.
// The Subject column on the events table is informational — it is not
// an Envelope field — so it is read off the row and discarded.
func scanEvent(rows driver.Rows) (sextantproto.Envelope, error) {
	var (
		id             uuid.UUID
		ts             time.Time
		subject        string
		fromKind       string
		fromID         string
		toKind         string
		toID           string
		traceID        uuid.UUID
		spanID         uuid.UUID
		parentSpanID   uuid.UUID
		kind           string
		protoVersion   string
		payloadStr     string
		idempotencyKey string
		replyTo        string
	)
	if err := rows.Scan(
		&id, &ts, &subject,
		&fromKind, &fromID, &toKind, &toID,
		&traceID, &spanID, &parentSpanID,
		&kind, &protoVersion,
		&payloadStr,
		&idempotencyKey, &replyTo,
	); err != nil {
		return sextantproto.Envelope{}, fmt.Errorf("scan: %w", err)
	}
	env := sextantproto.Envelope{
		ID:           id,
		Ts:           sextantproto.AtTimestamp(ts),
		ProtoVersion: protoVersion,
		From:         sextantproto.Address{Kind: sextantproto.AddressKind(fromKind), ID: fromID},
		TraceID:      traceID,
		SpanID:       spanID,
		Kind:         sextantproto.Kind(kind),
		Payload:      json.RawMessage(payloadStr),
	}
	if toKind != "" || toID != "" {
		to := sextantproto.Address{Kind: sextantproto.AddressKind(toKind), ID: toID}
		env.To = &to
	}
	if parentSpanID != uuid.Nil {
		ps := parentSpanID
		env.ParentSpanID = &ps
	}
	if idempotencyKey != "" {
		k := idempotencyKey
		env.IdempotencyKey = &k
	}
	if replyTo != "" {
		r := replyTo
		env.ReplyTo = &r
	}
	_ = subject // not part of the Envelope struct
	return env, nil
}

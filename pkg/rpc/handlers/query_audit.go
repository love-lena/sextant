package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// NewQueryAudit returns a Handler that queries the ClickHouse `audit`
// table directly. Columns mirror pkg/clickhouseboot/migrations/002-audit.sql:
// id, ts, actor, agent_uuid, action, capability_required, result, payload.
//
// The payload column is JSON — we project it as a raw string and let
// the caller json.Unmarshal it into sextantproto.AuditPayload if they
// want the structured shape.
func NewQueryAudit(db QueryHistoryDB) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.QueryAuditRequest
		if len(req.Payload) > 0 {
			if err := json.Unmarshal(req.Payload, &args); err != nil {
				return emitErr(emit, sextantproto.ErrCodeBadRequest,
					fmt.Sprintf("decode query_audit payload: %v", err))
			}
		}
		query, params := buildAuditQuery(args)
		rows, err := db.Query(ctx, query, params...)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("clickhouse query: %v", err))
		}
		defer func() { _ = rows.Close() }()

		out := make([]sextantproto.QueryAuditRow, 0)
		for rows.Next() {
			row, scanErr := scanAuditRow(rows)
			if scanErr != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("scan audit row: %v", scanErr))
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("clickhouse rows.Err: %v", err))
		}
		return emitOK(emit, sextantproto.QueryAuditResponse{Rows: out})
	}
}

func buildAuditQuery(args sextantproto.QueryAuditRequest) (string, []any) {
	cols := []string{
		"id", "ts", "actor", "agent_uuid",
		"action", "capability_required", "result",
		"toString(payload) AS payload_str",
	}
	var (
		clauses []string
		params  []any
	)
	if args.Filter.Actor != "" {
		clauses = append(clauses, "actor = ?")
		params = append(params, args.Filter.Actor)
	}
	if args.Filter.Action != "" {
		clauses = append(clauses, "action = ?")
		params = append(params, args.Filter.Action)
	}
	if args.Filter.AgentUUID != uuid.Nil {
		clauses = append(clauses, "agent_uuid = ?")
		params = append(params, args.Filter.AgentUUID.String())
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

	q := "SELECT " + strings.Join(cols, ", ") + " FROM audit"
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += " ORDER BY ts ASC LIMIT " + fmt.Sprintf("%d", limit)
	return q, params
}

func scanAuditRow(rows driver.Rows) (sextantproto.QueryAuditRow, error) {
	var (
		id                 uuid.UUID
		ts                 time.Time
		actor              string
		agentUUID          uuid.UUID
		action             string
		capabilityRequired string
		result             string
		payload            string
	)
	if err := rows.Scan(
		&id, &ts, &actor, &agentUUID,
		&action, &capabilityRequired, &result,
		&payload,
	); err != nil {
		return sextantproto.QueryAuditRow{}, fmt.Errorf("scan: %w", err)
	}
	return sextantproto.QueryAuditRow{
		ID:                 id,
		Ts:                 ts,
		Actor:              actor,
		AgentUUID:          agentUUID,
		Action:             action,
		CapabilityRequired: capabilityRequired,
		Result:             result,
		Payload:            payload,
	}, nil
}

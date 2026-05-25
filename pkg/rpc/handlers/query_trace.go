package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// NewQueryTrace returns a Handler that selects every span in
// `telemetry_traces` whose TraceId matches the request. Returns the
// spans ordered by Timestamp ASC so the CLI can render a chronological
// trace tree directly.
//
// The result is bounded by rpc.QueryHistoryMaxLimit rows — a single
// trace_id should fit well under that ceiling, but the cap keeps a
// runaway query from pulling unbounded data.
func NewQueryTrace(db QueryHistoryDB) rpc.Handler {
	return func(ctx context.Context, req sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		var args sextantproto.QueryTraceRequest
		if err := json.Unmarshal(req.Payload, &args); err != nil {
			return emitErr(emit, sextantproto.ErrCodeBadRequest,
				fmt.Sprintf("decode query_trace payload: %v", err))
		}
		if strings.TrimSpace(args.TraceID) == "" {
			return emitErr(emit, sextantproto.ErrCodeBadRequest, "trace_id is required")
		}
		cols := []string{
			"TraceId", "SpanId", "ParentSpanId",
			"SpanName", "SpanKind", "ServiceName",
			"Timestamp", "Duration",
			"StatusCode", "StatusMessage",
			"SpanAttributes",
		}
		q := "SELECT " + strings.Join(cols, ", ") +
			" FROM telemetry_traces WHERE TraceId = ?" +
			" ORDER BY Timestamp ASC LIMIT " +
			fmt.Sprintf("%d", rpc.QueryHistoryMaxLimit)

		rows, err := db.Query(ctx, q, args.TraceID)
		if err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("clickhouse query: %v", err))
		}
		defer func() { _ = rows.Close() }()

		out := make([]sextantproto.TraceSpan, 0)
		for rows.Next() {
			var (
				traceID, spanID, parentSpanID string
				spanName, spanKind, service   string
				ts                            time.Time
				duration                      int64
				statusCode, statusMessage     string
				attrs                         map[string]string
			)
			if err := rows.Scan(
				&traceID, &spanID, &parentSpanID,
				&spanName, &spanKind, &service,
				&ts, &duration,
				&statusCode, &statusMessage,
				&attrs,
			); err != nil {
				return emitErr(emit, sextantproto.ErrCodeInternal,
					fmt.Sprintf("scan trace row: %v", err))
			}
			out = append(out, sextantproto.TraceSpan{
				TraceID:       traceID,
				SpanID:        spanID,
				ParentSpanID:  parentSpanID,
				SpanName:      spanName,
				SpanKind:      spanKind,
				ServiceName:   service,
				Timestamp:     ts,
				DurationNanos: duration,
				StatusCode:    statusCode,
				StatusMessage: statusMessage,
				Attributes:    attrs,
			})
		}
		if err := rows.Err(); err != nil {
			return emitErr(emit, sextantproto.ErrCodeInternal,
				fmt.Sprintf("clickhouse rows.Err: %v", err))
		}
		return emitOK(emit, sextantproto.QueryTraceResponse{Spans: out})
	}
}

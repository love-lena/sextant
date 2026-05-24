package shipper

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// insertRows performs one ClickHouse INSERT for the supplied rows. All
// rows in the slice must share the same Table — the caller (the
// per-table flusher) guarantees this.
//
// We use clickhouse-go's PrepareBatch with the canonical INSERT
// statement per table; the column list is explicit so a schema change
// surfaces here as an Append-error rather than as silent column drop.
func insertRows(ctx context.Context, conn driver.Conn, table Table, rows []Row) error {
	if len(rows) == 0 {
		return nil
	}
	switch table {
	case TableEvents:
		return insertEvents(ctx, conn, rows)
	case TableAudit:
		return insertAudit(ctx, conn, rows)
	case TableTelemetryTraces:
		return insertTraces(ctx, conn, rows)
	case TableTelemetryMetrics:
		return insertMetrics(ctx, conn, rows)
	case TableTelemetryLogs:
		return insertLogs(ctx, conn, rows)
	default:
		return fmt.Errorf("shipper: unknown table %s", table)
	}
}

const insertEventsSQL = `INSERT INTO events (
    id, ts, subject, from_kind, from_id, to_kind, to_id,
    trace_id, span_id, parent_span_id, kind, proto_version,
    payload, idempotency_key, reply_to
)`

func insertEvents(ctx context.Context, conn driver.Conn, rows []Row) error {
	batch, err := conn.PrepareBatch(ctx, insertEventsSQL)
	if err != nil {
		return fmt.Errorf("shipper: prepare events batch: %w", err)
	}
	for _, r := range rows {
		ev := r.Event
		if ev == nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: nil Event row for table %s", r.Table)
		}
		if err := batch.Append(
			ev.ID, ev.Ts, ev.Subject,
			ev.FromKind, ev.FromID, ev.ToKind, ev.ToID,
			ev.TraceID, ev.SpanID, ev.ParentSpanID,
			ev.Kind, ev.ProtoVersion, ev.Payload,
			ev.IdempotencyKey, ev.ReplyTo,
		); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: append events row: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("shipper: send events batch: %w", err)
	}
	return nil
}

const insertAuditSQL = `INSERT INTO audit (
    id, ts, actor, agent_uuid, action,
    capability_required, result, payload
)`

func insertAudit(ctx context.Context, conn driver.Conn, rows []Row) error {
	batch, err := conn.PrepareBatch(ctx, insertAuditSQL)
	if err != nil {
		return fmt.Errorf("shipper: prepare audit batch: %w", err)
	}
	for _, r := range rows {
		ar := r.Audit
		if ar == nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: nil Audit row for table %s", r.Table)
		}
		if err := batch.Append(
			ar.ID, ar.Ts, ar.Actor, ar.AgentUUID,
			ar.Action, ar.CapabilityRequired, ar.Result, ar.Payload,
		); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: append audit row: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("shipper: send audit batch: %w", err)
	}
	return nil
}

const insertTracesSQL = `INSERT INTO telemetry_traces (
    Timestamp, TraceId, SpanId, ParentSpanId, TraceState,
    SpanName, SpanKind, ServiceName,
    ResourceAttributes, SpanAttributes,
    Duration, StatusCode, StatusMessage,
    Events.Timestamp, Events.Name, Events.Attributes,
    Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes
)`

func insertTraces(ctx context.Context, conn driver.Conn, rows []Row) error {
	batch, err := conn.PrepareBatch(ctx, insertTracesSQL)
	if err != nil {
		return fmt.Errorf("shipper: prepare traces batch: %w", err)
	}
	for _, r := range rows {
		tr := r.TelemetryTrace
		if tr == nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: nil TelemetryTrace row for table %s", r.Table)
		}
		// ClickHouse Nested columns are appended as parallel slices.
		// nil slices are accepted by clickhouse-go and emit empty
		// arrays, which is what we want for spans with no
		// events/links.
		if err := batch.Append(
			tr.Timestamp,
			tr.TraceID, tr.SpanID, tr.ParentSpanID, tr.TraceState,
			tr.SpanName, tr.SpanKind, tr.ServiceName,
			tr.ResourceAttributes, tr.SpanAttributes,
			tr.Duration, tr.StatusCode, tr.StatusMessage,
			tr.EventsTimestamp, tr.EventsName, tr.EventsAttributes,
			tr.LinksTraceID, tr.LinksSpanID, tr.LinksTraceState, tr.LinksAttributes,
		); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: append traces row: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("shipper: send traces batch: %w", err)
	}
	return nil
}

const insertMetricsSQL = `INSERT INTO telemetry_metrics (
    Timestamp, MetricName, MetricDescription, MetricUnit, MetricType,
    ServiceName, ResourceAttributes, Attributes,
    Value, Count, Sum, BucketCounts, ExplicitBounds
)`

func insertMetrics(ctx context.Context, conn driver.Conn, rows []Row) error {
	batch, err := conn.PrepareBatch(ctx, insertMetricsSQL)
	if err != nil {
		return fmt.Errorf("shipper: prepare metrics batch: %w", err)
	}
	for _, r := range rows {
		mr := r.TelemetryMetric
		if mr == nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: nil TelemetryMetric row for table %s", r.Table)
		}
		if err := batch.Append(
			mr.Timestamp, mr.MetricName, mr.MetricDescription,
			mr.MetricUnit, mr.MetricType, mr.ServiceName,
			mr.ResourceAttributes, mr.Attributes,
			mr.Value, mr.Count, mr.Sum,
			mr.BucketCounts, mr.ExplicitBounds,
		); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: append metrics row: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("shipper: send metrics batch: %w", err)
	}
	return nil
}

const insertLogsSQL = `INSERT INTO telemetry_logs (
    Timestamp, ObservedTimestamp, TraceId, SpanId,
    SeverityText, SeverityNumber, ServiceName, Body,
    ResourceAttributes, LogAttributes
)`

func insertLogs(ctx context.Context, conn driver.Conn, rows []Row) error {
	batch, err := conn.PrepareBatch(ctx, insertLogsSQL)
	if err != nil {
		return fmt.Errorf("shipper: prepare logs batch: %w", err)
	}
	for _, r := range rows {
		lr := r.TelemetryLog
		if lr == nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: nil TelemetryLog row for table %s", r.Table)
		}
		if err := batch.Append(
			lr.Timestamp, lr.ObservedTimestamp,
			lr.TraceID, lr.SpanID,
			lr.SeverityText, lr.SeverityNumber, lr.ServiceName, lr.Body,
			lr.ResourceAttributes, lr.LogAttributes,
		); err != nil {
			_ = batch.Abort()
			return fmt.Errorf("shipper: append logs row: %w", err)
		}
	}
	if err := batch.Send(); err != nil {
		return fmt.Errorf("shipper: send logs batch: %w", err)
	}
	return nil
}

package shipper

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// Table enumerates the ClickHouse target tables the shipper writes into.
// Each value also names the BoltDB bucket used as spillover (see
// spillover.go).
type Table string

const (
	TableEvents           Table = "events"
	TableAudit            Table = "audit"
	TableTelemetryTraces  Table = "telemetry_traces"
	TableTelemetryMetrics Table = "telemetry_metrics"
	TableTelemetryLogs    Table = "telemetry_logs"
)

// AllTables returns every Table the shipper knows about in canonical
// order. Used by the spillover and by tests.
func AllTables() []Table {
	return []Table{
		TableEvents,
		TableAudit,
		TableTelemetryTraces,
		TableTelemetryMetrics,
		TableTelemetryLogs,
	}
}

// SubjectMapping ties a JetStream subject pattern to a destination
// Table. The shipper creates one JetStream durable consumer per
// SubjectMapping. Durable names are derived from Consumer; keep them
// stable across restarts so the consumer resumes from its last ack.
type SubjectMapping struct {
	// Stream is the JetStream stream name (see pkg/natsboot/layout.go).
	Stream string
	// Subject is the consumer filter subject (must be a subset of the
	// stream's subjects).
	Subject string
	// Consumer is the durable consumer name used by JetStream.
	Consumer string
	// Table is the ClickHouse table this consumer's envelopes land in.
	Table Table
}

// DefaultMappings returns the canonical (subject pattern -> table)
// mapping defined in specs/components/shipper.md §"Subjects → tables
// mapping". Returning a fresh slice each call lets callers tweak it for
// tests without mutating shared state.
func DefaultMappings() []SubjectMapping {
	return []SubjectMapping{
		{Stream: "agent_frames", Subject: "agents.*.frames", Consumer: "shipper-agent-frames", Table: TableEvents},
		{Stream: "agent_lifecycle", Subject: "agents.*.lifecycle", Consumer: "shipper-agent-lifecycle", Table: TableEvents},
		{Stream: "agent_heartbeats", Subject: "agents.*.heartbeat", Consumer: "shipper-agent-heartbeats", Table: TableEvents},
		{Stream: "audit", Subject: "audit.>", Consumer: "shipper-audit", Table: TableAudit},
		{Stream: "telemetry_traces", Subject: "telemetry.traces.>", Consumer: "shipper-telemetry-traces", Table: TableTelemetryTraces},
		{Stream: "telemetry_metrics", Subject: "telemetry.metrics.>", Consumer: "shipper-telemetry-metrics", Table: TableTelemetryMetrics},
		{Stream: "telemetry_logs", Subject: "telemetry.logs.>", Consumer: "shipper-telemetry-logs", Table: TableTelemetryLogs},
		{Stream: "user_input", Subject: "user_input.>", Consumer: "shipper-user-input", Table: TableEvents},
	}
}

// Row is the union of every per-table row shape. A Row carries exactly
// one of the per-table fields set; the Table field discriminates.
// Concrete types per table avoid the cost of an `any`-typed batch.
type Row struct {
	Table Table

	// EnvelopeID and EnvelopeTs are populated for every Row; the metrics
	// goroutine reads them to compute lag.
	EnvelopeID uuid.UUID
	EnvelopeTs time.Time

	Event           *EventRow
	Audit           *AuditRow
	TelemetryTrace  *TelemetryTraceRow
	TelemetryMetric *TelemetryMetricRow
	TelemetryLog    *TelemetryLogRow
}

// EventRow mirrors the columns of the ClickHouse `events` table.
type EventRow struct {
	ID             uuid.UUID
	Ts             time.Time
	Subject        string
	FromKind       string
	FromID         string
	ToKind         string
	ToID           string
	TraceID        uuid.UUID
	SpanID         uuid.UUID
	ParentSpanID   uuid.UUID
	Kind           string
	ProtoVersion   string
	Payload        string
	IdempotencyKey string
	ReplyTo        string
}

// AuditRow mirrors the columns of the ClickHouse `audit` table.
type AuditRow struct {
	ID                 uuid.UUID
	Ts                 time.Time
	Actor              string
	AgentUUID          uuid.UUID
	Action             string
	CapabilityRequired string
	Result             string
	Payload            string
}

// TelemetryTraceRow mirrors the columns of the ClickHouse
// `telemetry_traces` table. Events and Links are flattened into the
// Nested-column layout ClickHouse expects.
type TelemetryTraceRow struct {
	Timestamp          time.Time
	TraceID            string
	SpanID             string
	ParentSpanID       string
	TraceState         string
	SpanName           string
	SpanKind           string
	ServiceName        string
	ResourceAttributes map[string]string
	SpanAttributes     map[string]string
	Duration           int64
	StatusCode         string
	StatusMessage      string
	EventsTimestamp    []time.Time
	EventsName         []string
	EventsAttributes   []map[string]string
	LinksTraceID       []string
	LinksSpanID        []string
	LinksTraceState    []string
	LinksAttributes    []map[string]string
}

// TelemetryMetricRow mirrors the columns of the ClickHouse
// `telemetry_metrics` table.
type TelemetryMetricRow struct {
	Timestamp          time.Time
	MetricName         string
	MetricDescription  string
	MetricUnit         string
	MetricType         string
	ServiceName        string
	ResourceAttributes map[string]string
	Attributes         map[string]string
	Value              float64
	Count              uint64
	Sum                float64
	BucketCounts       []uint64
	ExplicitBounds     []float64
}

// TelemetryLogRow mirrors the columns of the ClickHouse `telemetry_logs`
// table.
type TelemetryLogRow struct {
	Timestamp          time.Time
	ObservedTimestamp  time.Time
	TraceID            string
	SpanID             string
	SeverityText       string
	SeverityNumber     int32
	ServiceName        string
	Body               string
	ResourceAttributes map[string]string
	LogAttributes      map[string]string
}

// DecodeForTable parses raw envelope bytes against the supplied subject
// and target Table, returning the typed Row ready for batch insertion.
//
// The function fails fast on malformed envelopes (bad JSON, missing
// required fields). The caller (the consumer callback in shipper.go)
// should treat decode errors as terminal for the message and skip the
// ack so JetStream will redeliver — though in practice these errors
// represent a producer bug and will keep recurring.
func DecodeForTable(table Table, subject string, raw []byte) (Row, error) {
	var env sextantproto.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Row{}, fmt.Errorf("shipper: decode envelope: %w", err)
	}
	if err := env.Validate(); err != nil {
		return Row{}, fmt.Errorf("shipper: validate envelope: %w", err)
	}
	row := Row{
		Table:      table,
		EnvelopeID: env.ID,
		EnvelopeTs: env.Ts.Time,
	}
	switch table {
	case TableEvents:
		ev := buildEventRow(env, subject)
		row.Event = &ev
	case TableAudit:
		ar, err := buildAuditRow(env)
		if err != nil {
			return Row{}, err
		}
		row.Audit = &ar
	case TableTelemetryTraces:
		tr, err := buildTraceRow(env)
		if err != nil {
			return Row{}, err
		}
		row.TelemetryTrace = &tr
	case TableTelemetryMetrics:
		mr, err := buildMetricRow(env)
		if err != nil {
			return Row{}, err
		}
		row.TelemetryMetric = &mr
	case TableTelemetryLogs:
		lr, err := buildLogRow(env)
		if err != nil {
			return Row{}, err
		}
		row.TelemetryLog = &lr
	default:
		return Row{}, fmt.Errorf("shipper: unknown table %q", table)
	}
	return row, nil
}

// buildEventRow flattens an Envelope into the events table row shape.
// Every Envelope kind that routes to `events` (agent_frame, lifecycle,
// heartbeat, user_input_request/response) shares this projection.
func buildEventRow(env sextantproto.Envelope, subject string) EventRow {
	row := EventRow{
		ID:             env.ID,
		Ts:             env.Ts.Time,
		Subject:        subject,
		FromKind:       string(env.From.Kind),
		FromID:         env.From.ID,
		TraceID:        env.TraceID,
		SpanID:         env.SpanID,
		Kind:           string(env.Kind),
		ProtoVersion:   env.ProtoVersion,
		Payload:        string(env.Payload),
		IdempotencyKey: derefString(env.IdempotencyKey),
		ReplyTo:        derefString(env.ReplyTo),
	}
	if env.To != nil {
		row.ToKind = string(env.To.Kind)
		row.ToID = env.To.ID
	}
	if env.ParentSpanID != nil {
		row.ParentSpanID = *env.ParentSpanID
	}
	if len(row.Payload) == 0 {
		// ClickHouse JSON column rejects empty strings; use an empty
		// object so duplicate Envelopes with no payload still parse.
		row.Payload = "{}"
	}
	return row
}

// buildAuditRow projects an Envelope into the audit table row shape.
// Audit envelopes carry an AuditPayload whose fields map 1:1 to the
// audit columns.
func buildAuditRow(env sextantproto.Envelope) (AuditRow, error) {
	var payload sextantproto.AuditPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return AuditRow{}, fmt.Errorf("shipper: decode audit payload: %w", err)
	}
	row := AuditRow{
		ID:                 env.ID,
		Ts:                 env.Ts.Time,
		Actor:              payload.Actor,
		Action:             payload.Action,
		CapabilityRequired: payload.CapabilityRequired,
		Result:             string(payload.Result),
		Payload:            string(env.Payload),
	}
	if payload.AgentUUID != nil {
		row.AgentUUID = *payload.AgentUUID
	}
	if len(row.Payload) == 0 {
		row.Payload = "{}"
	}
	return row, nil
}

// buildTraceRow flattens an OTel-shaped Span payload into the
// telemetry_traces row layout. Span events and links are projected into
// parallel slices that match ClickHouse's Nested column representation.
func buildTraceRow(env sextantproto.Envelope) (TelemetryTraceRow, error) {
	var span sextantproto.Span
	if err := json.Unmarshal(env.Payload, &span); err != nil {
		return TelemetryTraceRow{}, fmt.Errorf("shipper: decode span: %w", err)
	}
	row := TelemetryTraceRow{
		Timestamp:          time.Unix(0, span.Timestamp).UTC(),
		TraceID:            span.TraceID,
		SpanID:             span.SpanID,
		ParentSpanID:       span.ParentSpanID,
		TraceState:         span.TraceState,
		SpanName:           span.SpanName,
		SpanKind:           string(span.SpanKind),
		ServiceName:        span.ServiceName,
		ResourceAttributes: cloneStringMap(span.ResourceAttributes),
		SpanAttributes:     cloneStringMap(span.SpanAttributes),
		Duration:           span.DurationNanos,
		StatusCode:         string(span.StatusCode),
		StatusMessage:      span.StatusMessage,
	}
	for _, e := range span.Events {
		row.EventsTimestamp = append(row.EventsTimestamp, time.Unix(0, e.TimestampNanos).UTC())
		row.EventsName = append(row.EventsName, e.Name)
		row.EventsAttributes = append(row.EventsAttributes, cloneStringMap(e.Attributes))
	}
	for _, l := range span.Links {
		row.LinksTraceID = append(row.LinksTraceID, l.TraceID)
		row.LinksSpanID = append(row.LinksSpanID, l.SpanID)
		row.LinksTraceState = append(row.LinksTraceState, l.TraceState)
		row.LinksAttributes = append(row.LinksAttributes, cloneStringMap(l.Attributes))
	}
	return row, nil
}

// buildMetricRow flattens an OTel-shaped Metric payload into the
// telemetry_metrics row layout.
func buildMetricRow(env sextantproto.Envelope) (TelemetryMetricRow, error) {
	var metric sextantproto.Metric
	if err := json.Unmarshal(env.Payload, &metric); err != nil {
		return TelemetryMetricRow{}, fmt.Errorf("shipper: decode metric: %w", err)
	}
	row := TelemetryMetricRow{
		Timestamp:          time.Unix(0, metric.Timestamp).UTC(),
		MetricName:         metric.MetricName,
		MetricDescription:  metric.MetricDescription,
		MetricUnit:         metric.MetricUnit,
		MetricType:         string(metric.MetricType),
		ServiceName:        metric.ServiceName,
		ResourceAttributes: cloneStringMap(metric.ResourceAttributes),
		Attributes:         cloneStringMap(metric.Attributes),
		Value:              metric.Value,
		Count:              metric.Count,
		Sum:                metric.Sum,
		BucketCounts:       append([]uint64(nil), metric.BucketCounts...),
		ExplicitBounds:     append([]float64(nil), metric.ExplicitBounds...),
	}
	return row, nil
}

// buildLogRow flattens an OTel-shaped LogRecord payload into the
// telemetry_logs row layout.
func buildLogRow(env sextantproto.Envelope) (TelemetryLogRow, error) {
	var log sextantproto.LogRecord
	if err := json.Unmarshal(env.Payload, &log); err != nil {
		return TelemetryLogRow{}, fmt.Errorf("shipper: decode log: %w", err)
	}
	row := TelemetryLogRow{
		Timestamp:          time.Unix(0, log.Timestamp).UTC(),
		ObservedTimestamp:  time.Unix(0, log.ObservedTimestamp).UTC(),
		TraceID:            log.TraceID,
		SpanID:             log.SpanID,
		SeverityText:       log.SeverityText,
		SeverityNumber:     log.SeverityNumber,
		ServiceName:        log.ServiceName,
		Body:               log.Body,
		ResourceAttributes: cloneStringMap(log.ResourceAttributes),
		LogAttributes:      cloneStringMap(log.LogAttributes),
	}
	return row, nil
}

// SubjectAuditCategory derives the audit subject category (last token
// after `audit.`) — used by mapping_test.go to assert category presence.
// Returns the empty string when subject is not under audit.>.
func SubjectAuditCategory(subject string) string {
	const prefix = "audit."
	if !strings.HasPrefix(subject, prefix) {
		return ""
	}
	return subject[len(prefix):]
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

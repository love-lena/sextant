package shipper

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestDefaultMappingsCoverEveryTable asserts the default mapping table
// has at least one mapping per declared Table. Regression guard: if a
// future spec change drops a mapping the test fails before runtime.
func TestDefaultMappingsCoverEveryTable(t *testing.T) {
	seen := map[Table]bool{}
	for _, m := range DefaultMappings() {
		seen[m.Table] = true
	}
	for _, tbl := range AllTables() {
		if !seen[tbl] {
			t.Errorf("no mapping targets table %s", tbl)
		}
	}
}

// TestDefaultMappingsConsumerNamesUnique catches accidental duplicate
// durable names. JetStream resolves duplicates server-side, but the
// resulting consumer "collision" is silent and hard to diagnose.
func TestDefaultMappingsConsumerNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range DefaultMappings() {
		if seen[m.Consumer] {
			t.Errorf("duplicate consumer name %q", m.Consumer)
		}
		seen[m.Consumer] = true
	}
}

// TestDecodeAgentFrameToEvents covers the AgentFrame → events route.
func TestDecodeAgentFrameToEvents(t *testing.T) {
	id := uuid.New()
	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: id.String()}
	to := sextantproto.Address{Kind: sextantproto.AddressUI, ID: "operator"}
	frame := sextantproto.AgentFramePayload{
		FrameKind: sextantproto.FrameAssistantText,
		Body:      map[string]any{"text": "hi"},
	}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from, frame)
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	env.To = &to
	idem := "key-123"
	env.IdempotencyKey = &idem
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	row, err := DecodeForTable(TableEvents, "agents.abc.frames", raw)
	if err != nil {
		t.Fatalf("DecodeForTable: %v", err)
	}
	if row.Table != TableEvents {
		t.Fatalf("table = %s", row.Table)
	}
	if row.Event == nil {
		t.Fatal("Event nil")
	}
	er := row.Event
	if er.ID != env.ID {
		t.Errorf("ID mismatch")
	}
	if er.Subject != "agents.abc.frames" {
		t.Errorf("Subject = %q", er.Subject)
	}
	if er.FromKind != "agent" || er.FromID != id.String() {
		t.Errorf("From = %s/%s", er.FromKind, er.FromID)
	}
	if er.ToKind != "ui" || er.ToID != "operator" {
		t.Errorf("To = %s/%s", er.ToKind, er.ToID)
	}
	if er.Kind != "agent_frame" {
		t.Errorf("Kind = %s", er.Kind)
	}
	if er.IdempotencyKey != "key-123" {
		t.Errorf("IdempotencyKey = %q", er.IdempotencyKey)
	}
	if er.ProtoVersion != sextantproto.ProtoVersion {
		t.Errorf("ProtoVersion = %s", er.ProtoVersion)
	}
	if er.Payload == "" || er.Payload == "{}" {
		t.Errorf("Payload not populated: %q", er.Payload)
	}
	if row.EnvelopeID != env.ID {
		t.Errorf("EnvelopeID mismatch")
	}
}

// TestDecodeAuditToAudit covers the Audit → audit-table route.
func TestDecodeAuditToAudit(t *testing.T) {
	agentID := uuid.New()
	from := sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "lena"}
	payload := sextantproto.AuditPayload{
		Actor:              "lena",
		AgentUUID:          &agentID,
		Action:             "spawn_agent",
		CapabilityRequired: "spawn",
		Result:             sextantproto.AuditAllowed,
	}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAudit, from, payload)
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	row, err := DecodeForTable(TableAudit, "audit.spawn", raw)
	if err != nil {
		t.Fatalf("DecodeForTable: %v", err)
	}
	if row.Audit == nil {
		t.Fatal("Audit nil")
	}
	ar := row.Audit
	if ar.Actor != "lena" || ar.Action != "spawn_agent" || ar.Result != "allowed" {
		t.Errorf("audit fields wrong: %+v", ar)
	}
	if ar.AgentUUID != agentID {
		t.Errorf("AgentUUID mismatch")
	}
	if ar.CapabilityRequired != "spawn" {
		t.Errorf("CapabilityRequired = %q", ar.CapabilityRequired)
	}
}

// TestDecodeTelemetrySpanToTraces covers the telemetry_span → traces
// route, including events + links flattening.
func TestDecodeTelemetrySpanToTraces(t *testing.T) {
	from := sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "daemon-host-a"}
	span := sextantproto.Span{
		Timestamp:     time.Now().UnixNano(),
		TraceID:       "trace-xyz",
		SpanID:        "span-001",
		ParentSpanID:  "span-000",
		SpanName:      "shipper.flush",
		SpanKind:      sextantproto.SpanKindInternal,
		ServiceName:   "sextant-shipper",
		DurationNanos: 123_456,
		StatusCode:    sextantproto.StatusCodeOK,
		SpanAttributes: map[string]string{
			"table": "events",
		},
		Events: []sextantproto.SpanEvent{{
			TimestampNanos: time.Now().UnixNano(),
			Name:           "batch_flushed",
			Attributes:     map[string]string{"rows": "100"},
		}},
		Links: []sextantproto.SpanLink{{
			TraceID: "other-trace",
			SpanID:  "other-span",
		}},
	}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindTelemetrySpan, from, span)
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	row, err := DecodeForTable(TableTelemetryTraces, "telemetry.traces.host-a", raw)
	if err != nil {
		t.Fatalf("DecodeForTable: %v", err)
	}
	if row.TelemetryTrace == nil {
		t.Fatal("TelemetryTrace nil")
	}
	tr := row.TelemetryTrace
	if tr.SpanName != "shipper.flush" {
		t.Errorf("SpanName = %s", tr.SpanName)
	}
	if tr.Duration != 123_456 {
		t.Errorf("Duration = %d", tr.Duration)
	}
	if len(tr.EventsName) != 1 || tr.EventsName[0] != "batch_flushed" {
		t.Errorf("EventsName = %v", tr.EventsName)
	}
	if len(tr.LinksTraceID) != 1 || tr.LinksTraceID[0] != "other-trace" {
		t.Errorf("LinksTraceID = %v", tr.LinksTraceID)
	}
}

// TestDecodeTelemetryMetricToMetrics covers the metric → metrics route.
func TestDecodeTelemetryMetricToMetrics(t *testing.T) {
	from := sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "daemon-host-a"}
	m := sextantproto.Metric{
		Timestamp:   time.Now().UnixNano(),
		MetricName:  "shipper.lag_seconds",
		MetricType:  sextantproto.MetricGauge,
		ServiceName: "sextant-shipper",
		Value:       0.42,
		Attributes:  map[string]string{"host": "h-a"},
	}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindTelemetryMetric, from, m)
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	row, err := DecodeForTable(TableTelemetryMetrics, "telemetry.metrics.host-a", raw)
	if err != nil {
		t.Fatalf("DecodeForTable: %v", err)
	}
	if row.TelemetryMetric == nil {
		t.Fatal("TelemetryMetric nil")
	}
	if row.TelemetryMetric.MetricName != "shipper.lag_seconds" {
		t.Errorf("MetricName = %s", row.TelemetryMetric.MetricName)
	}
	if row.TelemetryMetric.Value != 0.42 {
		t.Errorf("Value = %f", row.TelemetryMetric.Value)
	}
}

// TestDecodeTelemetryLogToLogs covers the log → logs route.
func TestDecodeTelemetryLogToLogs(t *testing.T) {
	from := sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "daemon-host-a"}
	now := time.Now().UnixNano()
	rec := sextantproto.LogRecord{
		Timestamp:         now,
		ObservedTimestamp: now,
		ServiceName:       "sextant-shipper",
		SeverityText:      "INFO",
		SeverityNumber:    9,
		Body:              "hello world",
	}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindTelemetryLog, from, rec)
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	row, err := DecodeForTable(TableTelemetryLogs, "telemetry.logs.host-a", raw)
	if err != nil {
		t.Fatalf("DecodeForTable: %v", err)
	}
	if row.TelemetryLog == nil {
		t.Fatal("TelemetryLog nil")
	}
	if row.TelemetryLog.Body != "hello world" {
		t.Errorf("Body = %s", row.TelemetryLog.Body)
	}
	if row.TelemetryLog.SeverityNumber != 9 {
		t.Errorf("SeverityNumber = %d", row.TelemetryLog.SeverityNumber)
	}
}

// TestDecodeInvalidEnvelope catches malformed envelopes early.
func TestDecodeInvalidEnvelope(t *testing.T) {
	if _, err := DecodeForTable(TableEvents, "agents.x.frames", []byte("not json")); err == nil {
		t.Fatal("expected error for non-JSON input")
	}
	// Validate() rejects empty Kind / nil IDs.
	if _, err := DecodeForTable(TableEvents, "agents.x.frames", []byte("{}")); err == nil {
		t.Fatal("expected validation error for empty envelope")
	}
}

// TestDecodeUnknownTable is an explicit guard so a future Table value
// added in mapping.go but missed in DecodeForTable surfaces in tests.
func TestDecodeUnknownTable(t *testing.T) {
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindHeartbeat,
		sextantproto.Address{Kind: sextantproto.AddressAgent, ID: "x"},
		sextantproto.HeartbeatPayload{AgentUUID: uuid.New(), IncarnationID: uuid.New(), HostID: "h"})
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := DecodeForTable(Table("nonexistent"), "x", raw); err == nil {
		t.Fatal("expected error for unknown table")
	}
}

// TestSubjectAuditCategory is a small helper test for the category
// extractor used by audit-only consumers.
func TestSubjectAuditCategory(t *testing.T) {
	cases := map[string]string{
		"audit.spawn":              "spawn",
		"audit.tool_call.send_msg": "tool_call.send_msg",
		"telemetry.traces.host":    "",
		"":                         "",
	}
	for in, want := range cases {
		if got := SubjectAuditCategory(in); got != want {
			t.Errorf("SubjectAuditCategory(%q) = %q, want %q", in, got, want)
		}
	}
}

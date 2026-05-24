package sextantproto

import (
	"encoding/json"
	"testing"
)

func TestSpanRoundTrip(t *testing.T) {
	s := Span{
		Timestamp:          1_700_000_000_000_000_000,
		TraceID:            "abc",
		SpanID:             "def",
		ParentSpanID:       "ghi",
		SpanName:           "rpc.list_agents",
		SpanKind:           SpanKindServer,
		ServiceName:        "sextantd",
		ResourceAttributes: map[string]string{"host.name": "host-a"},
		SpanAttributes:     map[string]string{"capability": "read.agents"},
		DurationNanos:      150_000_000,
		StatusCode:         StatusCodeOK,
		Events: []SpanEvent{{
			TimestampNanos: 1_700_000_000_000_000_000,
			Name:           "checkpoint",
			Attributes:     map[string]string{"phase": "1"},
		}},
		Links: []SpanLink{{TraceID: "x", SpanID: "y"}},
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Span
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.SpanName != s.SpanName || back.StatusCode != s.StatusCode {
		t.Fatalf("span roundtrip mismatch")
	}
	if len(back.Events) != 1 || back.Events[0].Name != "checkpoint" {
		t.Fatalf("span events roundtrip mismatch")
	}
	if back.SpanAttributes["capability"] != "read.agents" {
		t.Fatalf("span attrs roundtrip mismatch")
	}
}

func TestMetricRoundTrip(t *testing.T) {
	m := Metric{
		Timestamp:    1_700_000_000_000_000_000,
		MetricName:   "shipper.lag_seconds",
		MetricType:   MetricGauge,
		ServiceName:  "sextant-shipper",
		Attributes:   map[string]string{"stream": "audit"},
		Value:        0.42,
		Count:        100,
		Sum:          42,
		BucketCounts: []uint64{1, 2, 3},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Metric
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.MetricName != m.MetricName || back.Value != m.Value {
		t.Fatalf("metric roundtrip mismatch")
	}
	if len(back.BucketCounts) != 3 {
		t.Fatalf("buckets roundtrip mismatch")
	}
}

func TestLogRecordRoundTrip(t *testing.T) {
	l := LogRecord{
		Timestamp:      1_700_000_000_000_000_000,
		SeverityText:   "INFO",
		SeverityNumber: 9,
		ServiceName:    "sextantd",
		Body:           "starting nats",
		LogAttributes:  map[string]string{"phase": "boot"},
	}
	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back LogRecord
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Body != l.Body || back.SeverityText != l.SeverityText {
		t.Fatalf("log roundtrip mismatch")
	}
	if back.LogAttributes["phase"] != "boot" {
		t.Fatalf("log attrs roundtrip mismatch")
	}
}

package sextantproto

// OTel-shaped types. We match the OpenTelemetry data model field names
// and field semantics so a ClickHouse-side reader can swap between
// sextant's shipper and the OTel ClickHouse exporter without schema
// migration. Wire format is JSON for debuggability; precision uses
// int64 nanoseconds since unix epoch as in the OTel reference.
//
// We intentionally keep our own types here rather than importing the
// opentelemetry-go SDK: M1 must compile without an OTel runtime dep
// (architecture.md §8).

// Span is an OTel-compatible span record. Field names mirror the OTel
// ClickHouse exporter schema for `telemetry_traces`.
type Span struct {
	Timestamp          int64             `json:"timestamp_ns"`
	TraceID            string            `json:"trace_id"`
	SpanID             string            `json:"span_id"`
	ParentSpanID       string            `json:"parent_span_id,omitempty"`
	TraceState         string            `json:"trace_state,omitempty"`
	SpanName           string            `json:"span_name"`
	SpanKind           SpanKind          `json:"span_kind"`
	ServiceName        string            `json:"service_name"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
	SpanAttributes     map[string]string `json:"span_attributes,omitempty"`
	DurationNanos      int64             `json:"duration_ns"`
	StatusCode         StatusCode        `json:"status_code"`
	StatusMessage      string            `json:"status_message,omitempty"`
	Events             []SpanEvent       `json:"events,omitempty"`
	Links              []SpanLink        `json:"links,omitempty"`
}

// SpanKind matches the OTel SpanKind enum.
type SpanKind string

const (
	SpanKindUnspecified SpanKind = "SPAN_KIND_UNSPECIFIED"
	SpanKindInternal    SpanKind = "SPAN_KIND_INTERNAL"
	SpanKindServer      SpanKind = "SPAN_KIND_SERVER"
	SpanKindClient      SpanKind = "SPAN_KIND_CLIENT"
	SpanKindProducer    SpanKind = "SPAN_KIND_PRODUCER"
	SpanKindConsumer    SpanKind = "SPAN_KIND_CONSUMER"
)

// StatusCode matches the OTel status code enum.
type StatusCode string

const (
	StatusCodeUnset StatusCode = "STATUS_CODE_UNSET"
	StatusCodeOK    StatusCode = "STATUS_CODE_OK"
	StatusCodeError StatusCode = "STATUS_CODE_ERROR"
)

// SpanEvent is an OTel event attached to a span.
type SpanEvent struct {
	TimestampNanos int64             `json:"timestamp_ns"`
	Name           string            `json:"name"`
	Attributes     map[string]string `json:"attributes,omitempty"`
}

// SpanLink is an OTel cross-trace link.
type SpanLink struct {
	TraceID    string            `json:"trace_id"`
	SpanID     string            `json:"span_id"`
	TraceState string            `json:"trace_state,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// Metric is an OTel-compatible metric measurement. We carry the
// numeric value plus the optional histogram fields; the consumer
// distinguishes via MetricType.
type Metric struct {
	Timestamp          int64             `json:"timestamp_ns"`
	MetricName         string            `json:"metric_name"`
	MetricDescription  string            `json:"metric_description,omitempty"`
	MetricUnit         string            `json:"metric_unit,omitempty"`
	MetricType         MetricType        `json:"metric_type"`
	ServiceName        string            `json:"service_name"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
	Attributes         map[string]string `json:"attributes,omitempty"`
	Value              float64           `json:"value"`
	Count              uint64            `json:"count,omitempty"`
	Sum                float64           `json:"sum,omitempty"`
	BucketCounts       []uint64          `json:"bucket_counts,omitempty"`
	ExplicitBounds     []float64         `json:"explicit_bounds,omitempty"`
}

// MetricType enumerates the OTel metric type variants.
type MetricType string

const (
	MetricGauge     MetricType = "gauge"
	MetricSum       MetricType = "sum"
	MetricHistogram MetricType = "histogram"
	MetricSummary   MetricType = "summary"
)

// LogRecord is an OTel-compatible log record.
type LogRecord struct {
	Timestamp          int64             `json:"timestamp_ns"`
	ObservedTimestamp  int64             `json:"observed_timestamp_ns,omitempty"`
	TraceID            string            `json:"trace_id,omitempty"`
	SpanID             string            `json:"span_id,omitempty"`
	SeverityText       string            `json:"severity_text,omitempty"`
	SeverityNumber     int32             `json:"severity_number,omitempty"`
	ServiceName        string            `json:"service_name"`
	Body               string            `json:"body"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
	LogAttributes      map[string]string `json:"log_attributes,omitempty"`
}

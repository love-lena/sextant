-- 003-telemetry-traces.sql
-- OTel-shaped traces. Schema mirrors the OpenTelemetry ClickHouse
-- exporter's `otel_traces` table so we can swap the shipper for the
-- upstream exporter later without schema migration.
-- Spec: specs/components/clickhouse.md §"telemetry_traces"
CREATE TABLE IF NOT EXISTS telemetry_traces (
    Timestamp DateTime64(9),
    TraceId String,
    SpanId String,
    ParentSpanId String,
    TraceState String,
    SpanName LowCardinality(String),
    SpanKind LowCardinality(String),
    ServiceName LowCardinality(String),
    ResourceAttributes Map(LowCardinality(String), String),
    SpanAttributes Map(LowCardinality(String), String),
    Duration Int64,
    StatusCode LowCardinality(String),
    StatusMessage String,
    Events Nested(
        Timestamp DateTime64(9),
        Name LowCardinality(String),
        Attributes Map(LowCardinality(String), String)
    ),
    Links Nested(
        TraceId String,
        SpanId String,
        TraceState String,
        Attributes Map(LowCardinality(String), String)
    ),
    INDEX idx_trace_id TraceId TYPE bloom_filter GRANULARITY 4,
    INDEX idx_service ServiceName TYPE bloom_filter GRANULARITY 4
) ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (ServiceName, SpanName, toUnixTimestamp(Timestamp))
TTL toDateTime(Timestamp) + INTERVAL 30 DAY;

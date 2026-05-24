-- 005-telemetry-logs.sql
-- OTel-shaped logs. Schema mirrors the OpenTelemetry ClickHouse exporter's
-- `otel_logs` table.
-- Spec: specs/components/clickhouse.md §"telemetry_logs"
CREATE TABLE IF NOT EXISTS telemetry_logs (
    Timestamp DateTime64(9),
    ObservedTimestamp DateTime64(9),
    TraceId String,
    SpanId String,
    SeverityText LowCardinality(String),
    SeverityNumber Int32,
    ServiceName LowCardinality(String),
    Body String,
    ResourceAttributes Map(LowCardinality(String), String),
    LogAttributes Map(LowCardinality(String), String),
    INDEX idx_severity SeverityNumber TYPE minmax GRANULARITY 4,
    INDEX idx_service ServiceName TYPE bloom_filter GRANULARITY 4
) ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (ServiceName, toUnixTimestamp(Timestamp))
TTL toDateTime(Timestamp) + INTERVAL 30 DAY;

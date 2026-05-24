-- 004-telemetry-metrics.sql
-- OTel-shaped metrics. Field layout follows the OpenTelemetry ClickHouse
-- exporter's `otel_metrics_*` family but collapsed into one table with a
-- MetricType discriminator so query helpers stay simple.
-- Spec: specs/components/clickhouse.md §"telemetry_metrics", architecture.md §8
CREATE TABLE IF NOT EXISTS telemetry_metrics (
    Timestamp DateTime64(9),
    MetricName LowCardinality(String),
    MetricDescription String,
    MetricUnit LowCardinality(String),
    MetricType LowCardinality(String),
    ServiceName LowCardinality(String),
    ResourceAttributes Map(LowCardinality(String), String),
    Attributes Map(LowCardinality(String), String),
    Value Float64,
    Count UInt64,
    Sum Float64,
    BucketCounts Array(UInt64),
    ExplicitBounds Array(Float64),
    INDEX idx_metric MetricName TYPE bloom_filter GRANULARITY 4,
    INDEX idx_service ServiceName TYPE bloom_filter GRANULARITY 4
) ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (ServiceName, MetricName, toUnixTimestamp(Timestamp))
TTL toDateTime(Timestamp) + INTERVAL 90 DAY;

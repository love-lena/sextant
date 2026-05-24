-- 001-events.sql
-- Generic bus events table — every Envelope shipped from NATS lands here.
-- ReplacingMergeTree(ts) keeps the newest row per primary key on background
-- merge so duplicate shipper inserts collapse. Strict-dedup queries must
-- use FINAL or argMax(); the spec accepts the eventual-consistency tradeoff.
-- Spec: specs/components/clickhouse.md §"events"
CREATE TABLE IF NOT EXISTS events (
    id UUID,
    ts DateTime64(6),
    subject String,
    from_kind LowCardinality(String),
    from_id String,
    to_kind LowCardinality(String),
    to_id String,
    trace_id UUID,
    span_id UUID,
    parent_span_id UUID,
    kind LowCardinality(String),
    proto_version LowCardinality(String),
    payload JSON,
    idempotency_key String,
    reply_to String,
    INDEX idx_subject subject TYPE bloom_filter GRANULARITY 4,
    INDEX idx_trace_id trace_id TYPE bloom_filter GRANULARITY 4
) ENGINE = ReplacingMergeTree(ts)
ORDER BY (id);

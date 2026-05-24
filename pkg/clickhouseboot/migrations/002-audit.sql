-- 002-audit.sql
-- Auth-relevant actions. Long-retention table for forensics.
-- Spec: specs/components/clickhouse.md §"audit"
CREATE TABLE IF NOT EXISTS audit (
    id UUID,
    ts DateTime64(6),
    actor String,
    agent_uuid UUID,
    action LowCardinality(String),
    capability_required LowCardinality(String),
    result LowCardinality(String),
    payload JSON
) ENGINE = MergeTree()
ORDER BY (ts, actor, agent_uuid);

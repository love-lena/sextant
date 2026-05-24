-- 006-agent-definitions-history.sql
-- Every AgentDefinition mutation produces a row here. Permanent retention.
-- Spec: specs/components/clickhouse.md §"agent_definitions_history"
CREATE TABLE IF NOT EXISTS agent_definitions_history (
    agent_uuid UUID,
    version UInt64,
    ts DateTime64(6),
    actor String,
    change_kind LowCardinality(String),
    definition JSON
) ENGINE = MergeTree()
ORDER BY (agent_uuid, version);

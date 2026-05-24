package natsboot

import "time"

// StreamSpec captures a JetStream stream definition. Subjects use NATS'
// dot-separated hierarchy with `*` (single token) and `>` (one-or-more
// tokens) wildcards. Multi-token subjects must use `>` per
// specs/protocols/bus-subjects.md "Wildcard rules".
type StreamSpec struct {
	Name      string
	Subjects  []string
	MaxAge    time.Duration
	MaxBytes  int64
	Retention RetentionPolicy
}

// RetentionPolicy mirrors nats jetstream.RetentionPolicy as a small enum
// we control. Avoids leaking the upstream constant set into our spec.
type RetentionPolicy string

const (
	RetentionLimits   RetentionPolicy = "limits"
	RetentionInterest RetentionPolicy = "interest"
	RetentionWorkQ    RetentionPolicy = "workq"
)

// KVSpec captures a JetStream KV bucket definition.
type KVSpec struct {
	Bucket      string
	History     uint8         // values retained per key
	TTL         time.Duration // value TTL; 0 means no TTL
	Description string
}

// Streams returns the full list of streams M2 creates. Each entry maps to
// a row in specs/components/nats.md §"Streams to create".
func Streams(maxBytes int64) []StreamSpec {
	return []StreamSpec{
		{
			Name:      "agent_frames",
			Subjects:  []string{"agents.*.frames"},
			MaxAge:    7 * 24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "agent_lifecycle",
			Subjects:  []string{"agents.*.lifecycle"},
			MaxAge:    30 * 24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "agent_heartbeats",
			Subjects:  []string{"agents.*.heartbeat"},
			MaxAge:    1 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "agent_inbox",
			Subjects:  []string{"agents.*.inbox"},
			MaxAge:    24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "audit",
			Subjects:  []string{"audit.>"},
			MaxAge:    365 * 24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "telemetry_traces",
			Subjects:  []string{"telemetry.traces.>"},
			MaxAge:    7 * 24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "telemetry_metrics",
			Subjects:  []string{"telemetry.metrics.>"},
			MaxAge:    30 * 24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "telemetry_logs",
			Subjects:  []string{"telemetry.logs.>"},
			MaxAge:    7 * 24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "user_input",
			Subjects:  []string{"user_input.>"},
			MaxAge:    30 * 24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "control_rpc",
			Subjects:  []string{"sextant.rpc.>"},
			MaxAge:    24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
		{
			Name:      "system",
			Subjects:  []string{"sextant.system.>"},
			MaxAge:    30 * 24 * time.Hour,
			MaxBytes:  maxBytes,
			Retention: RetentionLimits,
		},
	}
}

// KVBuckets returns the full list of KV buckets M2 creates. Each entry
// maps to a row in specs/components/nats.md §"KV stores to create".
func KVBuckets() []KVSpec {
	return []KVSpec{
		{Bucket: "agent_definitions", History: 5, Description: "current agent definition per UUID; watchable"},
		{Bucket: "templates", History: 1, Description: "agent templates seeded from ~/.config/sextant/templates/"},
		{Bucket: "viz_specs", History: 1, Description: "visualization specs (post-M17)"},
		{Bucket: "ui_state", History: 1, Description: "inter-UI coordination (per-operator-scoped keys)"},
		{Bucket: "worktrees", History: 1, Description: "worktree registry"},
		{Bucket: "locks", History: 1, TTL: 0, Description: "mutual-exclusion locks (TTL set per-key by callers)"},
		{Bucket: "test_envs", History: 1, Description: "test environment registry"},
	}
}

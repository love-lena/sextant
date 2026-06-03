// Package sx holds the names of the reserved `sx` namespace — the subjects and
// KV buckets that belong to Sextant. The rule (ADR-0012): the sx namespace is
// Sextant's; everything else is yours.
//
// Two prefixes, because NATS forbids dots in KV bucket names: subjects are
// dotted (sx.control.*), buckets are underscored (sx_clients).
package sx

// Reserved KV buckets.
const (
	// BucketClients holds the clients registry: one record per client.
	BucketClients = "sx_clients"
	// BucketWorkflows holds workflow state envelopes, keyed by workflow id.
	BucketWorkflows = "sx_workflows"
)

// Operator-only system state is deferred: v1 has no operator-only bucket (the
// only system datum, the protocol epoch, is public). When real operator-only
// state exists it goes in a separate NATS account — a hard, enumerate-nothing
// split — not a same-account bucket guarded by deny-lists (ADR-0012).

// Reserved subjects.
const (
	// ControlPrefix is the operator-only control space.
	ControlPrefix = "sx.control."
	// SubjectDrain is the cooperative-drain broadcast (operator → clients).
	SubjectDrain = "sx.control.drain"
	// WorkflowPrefix is the workflow convention space.
	WorkflowPrefix = "sx.workflow."
)

// WorkflowControl is the control subject for a given workflow id.
func WorkflowControl(id string) string { return WorkflowPrefix + id + ".control" }

// WorkflowEvents is the event-stream subject for a given workflow id.
func WorkflowEvents(id string) string { return WorkflowPrefix + id + ".events" }

// Buckets returns the reserved buckets created at bootstrap, with the history
// depth each keeps.
func Buckets() []BucketSpec {
	return []BucketSpec{
		{Name: BucketClients, History: 1},    // registry: latest record per client
		{Name: BucketWorkflows, History: 10}, // workflow state: a little version history
	}
}

// BucketSpec describes a reserved bucket to bootstrap.
type BucketSpec struct {
	Name    string
	History uint8
}

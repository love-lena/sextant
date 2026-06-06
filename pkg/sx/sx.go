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
	// BucketMeta holds public protocol metadata that clients read at connect —
	// currently just the protocol epoch. It is client-readable by design: the
	// only system datum so far is public, so it lives in a client-readable
	// bucket, not an operator-only one (ADR-0015).
	BucketMeta = "sx_meta"
)

// MetaKeyEpoch is the key in BucketMeta holding the bus's protocol epoch, as a
// decimal string. The bus writes it at bootstrap; clients read and hard-gate on
// it at connect (ADR-0010).
const MetaKeyEpoch = "epoch"

// Operator-only system state is deferred: v1 has no operator-only bucket (the
// only system datum, the protocol epoch, is public — it lives in BucketMeta).
// When real operator-only state exists it goes in a separate NATS account — a
// hard, enumerate-nothing split — not a same-account bucket guarded by
// deny-lists (ADR-0012, ADR-0015).

// Reserved subjects.
const (
	// ControlPrefix is the operator-only control space.
	ControlPrefix = "sx.control."
	// SubjectDrain is a reserved operator-only control subject. Cooperative drain
	// is now delivered per-client over sx.deliver.<id>.drain (ADR-0019); this name
	// remains reserved in the operator-only control space.
	SubjectDrain = "sx.control.drain"
	// WorkflowPrefix is the workflow convention space.
	WorkflowPrefix = "sx.workflow."
)

// WorkflowControl is the control subject for a given workflow id.
func WorkflowControl(id string) string { return WorkflowPrefix + id + ".control" }

// WorkflowEvents is the event-stream subject for a given workflow id.
func WorkflowEvents(id string) string { return WorkflowPrefix + id + ".events" }

// The Messages convention. These subjects are user space (not reserved), but
// the durable stream that captures them is Sextant-managed infrastructure,
// provisioned by the operator at bootstrap.
const (
	// StreamMessages is the durable JetStream stream capturing MessagePrefix.
	StreamMessages = "MESSAGES"
	// MessagePrefix is the root of the messages subject space (msg.>).
	MessagePrefix = "msg."
	// BucketArtifacts is the KV bucket holding artifacts (keyed by name). It is
	// operator-provisioned (clients can't create buckets) and client-writable.
	BucketArtifacts = "ARTIFACTS"
	// ArtifactHistory is how many revisions each artifact keeps — 64, the NATS
	// KV maximum, so the version trail is as deep as the backend allows.
	ArtifactHistory = 64
)

// TopicSubject is the subject for a named topic: msg.topic.<name>. A topic is a
// shared room many clients publish to and subscribe to — a naming convention
// over the messages space, not a bus construct (no registry, membership, or
// access control). "Channel" is reserved for the Claude Code harness mechanism.
func TopicSubject(name string) string { return MessagePrefix + "topic." + name }

// ClientSubject is the direct subject for a client: msg.client.<id>.
func ClientSubject(id string) string { return MessagePrefix + "client." + id }

// Buckets returns the reserved buckets created at bootstrap, with the history
// depth each keeps.
func Buckets() []BucketSpec {
	return []BucketSpec{
		{Name: BucketClients, History: 1},    // registry: latest record per client
		{Name: BucketWorkflows, History: 10}, // workflow state: a little version history
		{Name: BucketMeta, History: 1},       // public protocol metadata (epoch)
	}
}

// BucketSpec describes a reserved bucket to bootstrap.
type BucketSpec struct {
	Name    string
	History uint8
}

// Package wireapi defines the Wire API: the client↔bus call protocol (ADR-0019
// §1). A client invokes an operation by making a NATS request to
// sx.api.<clientID>.<operation> and gets a Response; the bus serves it against
// the backend interface and stamps the frame. This package holds the subject
// scheme, the operation names (mirroring protocol/methods.json), and the
// per-operation request/response shapes shared by the bus and the SDK.
//
// It is internal plumbing: the SDK wraps it, so a client program never imports
// these types. The subject token <clientID> is the call's author: the per-client
// allow-list credential (ADR-0019) lets a client publish only under its own
// sx.api.<id> prefix, so the token is also its authenticated identity — which is
// what makes the bus-stamped author unforgeable.
package wireapi

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/love-lena/sextant/pkg/wire"
)

// APIPrefix is the root of the client→bus call space: sx.api.<clientID>.<op>.
const APIPrefix = "sx.api."

// WildcardSubject is what the bus subscribes to in order to receive every call.
const WildcardSubject = APIPrefix + "*.>"

// DeliverPrefix is the root of the bus→client push space (subscribe/watch
// delivery): sx.deliver.<clientID>.<stream>. Owner-subscribe only.
const DeliverPrefix = "sx.deliver."

// InboxPrefix is a client's private request/reply inbox prefix:
// _INBOX.<clientID>. The SDK sets it as the connection's custom inbox (so its
// call replies land under it) and the credential allow-lists subscribing only to
// <prefix>.>. This is per-client on purpose: the default shared _INBOX.> would
// let any client subscribe the wildcard and eavesdrop on every other client's
// call replies. The bus replies (on its operator connection) to whatever inbox a
// request carried, so it needs no knowledge of the prefix. The returned value
// has no trailing dot — nats.CustomInboxPrefix appends ".<token>" itself.
func InboxPrefix(clientID string) string { return "_INBOX." + clientID }

// DisplayNameTag is the JWT tag prefix carrying a client's human display_name,
// minted into the credential by the bus so the SDK reads it from the same
// credential the bus authenticated (it cannot diverge from the identity). The
// value is hex-encoded because NATS lowercases raw JWT tags, which would mangle a
// mixed-case or spaced display_name; lowercase hex survives that round-trip.
const DisplayNameTag = "display_name:"

// EncodeDisplayNameTag builds the JWT tag carrying name.
func EncodeDisplayNameTag(name string) string {
	return DisplayNameTag + hex.EncodeToString([]byte(name))
}

// DecodeDisplayNameTag returns the display_name carried by tag, if it is one.
func DecodeDisplayNameTag(tag string) (name string, ok bool) {
	v, found := strings.CutPrefix(tag, DisplayNameTag)
	if !found {
		return "", false
	}
	b, err := hex.DecodeString(v)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// Operation names — the protocol's operations (protocol/methods.json).
const (
	OpMessagePublish   = "message.publish"
	OpMessageRead      = "message.read"
	OpMessageSubscribe = "message.subscribe"
	OpArtifactCreate   = "artifact.create"
	OpArtifactUpdate   = "artifact.update"
	OpArtifactGet      = "artifact.get"
	OpArtifactList     = "artifact.list"
	OpArtifactDelete   = "artifact.delete"
	OpArtifactWatch    = "artifact.watch"
	OpClientsList      = "clients.list"
	OpClientsRegister  = "clients.register"
	OpClientsRetire    = "clients.retire"
)

// OpSubscriptionStop is the internal control op that ends a push-stream
// subscription (message.subscribe / artifact.watch). It is bus plumbing, not one
// of the protocol's operations (it is not in methods.json and has no CLI/MCP
// surface): the SDK calls it from Subscription.Stop / Watch.Stop to tear down the
// server-side relay it started.
const OpSubscriptionStop = "subscription.stop"

// OpPrincipalGet, OpPrincipalSet, and OpPrincipalWatch are the principal-
// designation ops (ADR-0030). They are an opinionated EXTENSION over the locked
// core — not protocol operations: they are not in methods.json (the universal
// protocol stays principal-free, ADR-0012/0022) and have no conformance entry. A
// fork may omit them.
//
//   - principal.get reads the current principal ULID (the client-readable side of
//     the sx_meta/principal key). Any authenticated caller may read it.
//   - principal.set re-points the principal. It is OPERATOR-ONLY: the bus rejects
//     any caller that is not the reserved operator identity, mirroring
//     clients.retire's gate — so the write-operator half of the key's shape is
//     enforced at the bus, never inferred from a forgeable field.
//   - principal.watch is a push-stream (like artifact.watch): it relays the
//     current value first, then each later change, into the caller's delivery
//     space — so a connected client observes a re-designation without
//     reconnecting (the discover-on-connect half rides clients.hello).
const (
	OpPrincipalGet   = "principal.get"
	OpPrincipalSet   = "principal.set"
	OpPrincipalWatch = "principal.watch"
)

// OpClientsHello is the internal connect-handshake op (ADR-0020). It is bus
// plumbing, not one of the protocol's operations (not in methods.json, no
// CLI/MCP surface): the SDK calls it once on Connect to confirm the caller is a
// known (issued, not retired) identity and to fold the protocol-epoch hard-gate
// into one round-trip — it returns the bus epoch (the SDK exact-matches) and the
// bus-stamped server time (the SDK clock-skew-checks). Presence is NOT asserted
// here: the bus derives online/offline from the live connection, so connecting
// (and disconnecting) needs no registry write at all.
const OpClientsHello = "clients.hello"

// OpClientsHeartbeat is the client's periodic liveness signal (TASK-126). Like
// clients.hello it is bus plumbing, NOT one of the protocol's operations (not in
// methods.json, no CLI/MCP surface, no conformance entry) — presence/liveness is
// reference-bus behaviour (ADR-0020), so a fork may omit it and the op is
// epoch-neutral (additive: it adds no frame shape). The SDK calls it on a timer;
// the bus records a bus-stamped last_seen on the client's registry record (the
// presence source that, unlike Connz, works across a leaf link) AND echoes the
// beat down the caller's delivery path so the client can confirm its own push
// path is live (the TASK-124 mode-D floor). A bus that does not implement it
// answers "unknown operation"; the SDK MUST treat that as benign — stop
// heartbeating and let presence fall back to the connection table — never crash.
const OpClientsHeartbeat = "clients.heartbeat"

// OperatorID and EnrollID are the two reserved infrastructure identities that
// authorize the issuance path (ADR-0020). They are not minted client ULIDs and
// never appear in the clients directory; they exist only to satisfy
// clients.register's "you must already be someone" exception:
//   - OperatorID — the human operator at the terminal. `sextant up` provisions
//     its credential in the store (held-identity mode: mint for another, and
//     retire).
//   - EnrollID — the bootstrap/enrollment identity. `sextant up` provisions its
//     credential too; a local process reads it (locality trust) to self-enroll.
//
// The bus authorizes clients.register from either of these — and, with its own
// authority, from any non-spawned client (mint-on-behalf, ADR-0033) — but
// clients.retire from the operator only.
const (
	OperatorID = "operator"
	EnrollID   = "enroll"
)

// Client kinds — the self-declared role a registration carries (RegisterInput.Kind).
// Kind is open (a fork may use any label), but one is load-bearing in the
// reference bus: KindAgent marks an auto-minting harness identity (ADR-0029) —
// the one kind that may NEVER be named the principal, which the bus rejects on
// the open enrollment claim path (ADR-0031), keeping agents off the principal by
// construction. KindClient is the default human-seat label (`register --self`).
//
// Note that the authority to mint-on-behalf (ADR-0033) is NOT a kind: kind is
// self-declared and weakly enforced, so the bus gates dispatching on the
// bus-stamped ClientEntry.SpawnedBy marker instead, never on kind.
const (
	KindClient = "client"
	KindAgent  = "agent"
)

// DrainSubID is the reserved sub-id for the cooperative-drain delivery on a
// client's push space (sx.deliver.<id>.drain). It is not a real relay, so a
// client may not use it as a message/artifact subscription id.
const DrainSubID = "drain"

// CallSubject builds the request subject for clientID invoking op.
func CallSubject(clientID, op string) string { return APIPrefix + clientID + "." + op }

// DeliverSubject builds the push-delivery subject for one subscription:
// sx.deliver.<clientID>.<subID>. The SDK subscribes to it before making the
// subscribe/watch call (so no delivery races the subscription), and the bus
// publishes each delivery to it. subID is a client-generated ULID, unique per
// subscription, which keeps deliveries from two local subscriptions on one
// connection from cross-wiring.
func DeliverSubject(clientID, subID string) string {
	return DeliverPrefix + clientID + "." + subID
}

// ParseCallSubject splits sx.api.<clientID>.<op> into its parts. The operation
// may itself contain dots (e.g. "message.publish").
func ParseCallSubject(subject string) (clientID, op string, ok bool) {
	rest, found := strings.CutPrefix(subject, APIPrefix)
	if !found {
		return "", "", false
	}
	i := strings.IndexByte(rest, '.')
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// Response wraps every call reply: Error is set on failure (and Result empty),
// otherwise Result holds the operation's output JSON.
type Response struct {
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// --- message.publish ---

// PublishInput is the input to message.publish; the client supplies only the
// subject and record (the bus stamps the frame).
type PublishInput struct {
	Subject string          `json:"subject"`
	Record  json.RawMessage `json:"record"`
}

// PublishOutput is the result of message.publish.
type PublishOutput struct {
	ID  string `json:"id"`
	Seq uint64 `json:"seq"`
}

// --- message.read ---

// ReadInput is the input to message.read (pull-batch; cursor = since).
type ReadInput struct {
	Subject string `json:"subject"`
	Since   uint64 `json:"since"`
	Limit   int    `json:"limit"`
}

// ReadOutput is a batch of stamped frames plus the cursor to resume from.
type ReadOutput struct {
	Messages   []wire.Frame `json:"messages"`
	NextCursor uint64       `json:"next_cursor"`
}

// --- artifact.create / artifact.update ---

// ArtifactCreateInput creates a new artifact from a record.
type ArtifactCreateInput struct {
	Name   string          `json:"name"`
	Record json.RawMessage `json:"record"`
}

// ArtifactUpdateInput compare-and-set updates an artifact at expected_rev.
type ArtifactUpdateInput struct {
	Name        string          `json:"name"`
	Record      json.RawMessage `json:"record"`
	ExpectedRev uint64          `json:"expected_rev"`
}

// ArtifactWriteOutput is the result of a create or update.
type ArtifactWriteOutput struct {
	Name     string `json:"name"`
	Revision uint64 `json:"revision"`
}

// --- artifact.get / artifact.delete ---

// ArtifactGetInput reads an artifact by name.
type ArtifactGetInput struct {
	Name string `json:"name"`
}

// ArtifactGetOutput is an artifact's current value and bus-stamped metadata.
type ArtifactGetOutput struct {
	Name      string          `json:"name"`
	Record    json.RawMessage `json:"record"`
	Revision  uint64          `json:"revision"`
	CreatedAt string          `json:"createdAt"`
	UpdatedAt string          `json:"updatedAt"`
}

// ArtifactDeleteInput removes an artifact by name.
type ArtifactDeleteInput struct {
	Name string `json:"name"`
}

// --- artifact.list ---

// ArtifactListInput carries no fields: artifact.list returns every artifact in
// the ARTIFACTS bucket (no filter — a client lists, then artifact.gets the one
// it wants).
type ArtifactListInput struct{}

// ArtifactListOutput is the artifacts directory: the name and bus-stamped
// metadata of every artifact, sorted by name. It carries no records — discovery
// of what exists, not the contents (those come from artifact.get).
type ArtifactListOutput struct {
	Artifacts []ArtifactListEntry `json:"artifacts"`
}

// ArtifactListEntry is one entry in the artifacts directory: an artifact's name
// and bus-stamped metadata (its current revision and the create/update times),
// but not its record. CreatedAt and UpdatedAt are RFC3339 strings, as in
// ArtifactGetOutput.
type ArtifactListEntry struct {
	Name      string `json:"name"`
	Revision  uint64 `json:"revision"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// --- clients.list ---

// ClientsListOutput is the clients registry directory.
type ClientsListOutput struct {
	Clients []ClientEntry `json:"clients"`
}

// ClientEntry is one entry in the clients directory (ADR-0020). At rest it is the
// durable identity record the bus persists in sx_clients, written once at
// issuance (clients.register) and removed only by retire — it survives disconnect
// and bus restart. In a clients.list reply it carries, in addition, the derived
// Presence ("online"/"offline"), which the bus computes from the live connection
// rather than from any stored field. ID is the bus-minted ULID; Subject is the
// authenticated public key the bus joins against the live connection table for
// presence (internal — omitted from list replies); IssuedAt is when the identity
// was minted.
type ClientEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	Epoch       int    `json:"epoch"`
	SDK         string `json:"sdk,omitempty"`
	IssuedAt    string `json:"issued_at"`
	Subject     string `json:"subject,omitempty"`
	Presence    string `json:"presence,omitempty"`
	// LastSeen is the bus-stamped time of the client's most recent heartbeat
	// (clients.heartbeat, TASK-126), RFC3339. Unlike Presence-from-Connz it is a
	// stored field that survives across a leaf link, so it is the leaf-correct
	// presence source: the bus derives online = last_seen within the freshness
	// window. Empty for an identity that has never heartbeated (e.g. a pre-TASK-126
	// client) — presence then falls back to the connection table.
	LastSeen string `json:"last_seen,omitempty"`
	// SpawnedBy is the id of the client that minted this identity via mint-on-behalf
	// (ADR-0033). Empty for a top-level identity (operator/enrollment-minted). The
	// bus stamps it at issuance; it is both the spawn lineage and the marker that
	// fences a spawned worker out of dispatching its own children — so the no-mint
	// guardrail rests on a bus-set field, never on the weakly-enforced kind.
	SpawnedBy string `json:"spawned_by,omitempty"`
}

// Presence values for ClientEntry.Presence.
const (
	PresenceOnline  = "online"
	PresenceOffline = "offline"
)

// --- message.subscribe (push-stream over sx.deliver.<id>.<sub_id>) ---

// SubscribeInput starts a push-stream subscription. SubID is the
// client-generated ULID naming the delivery subject; DeliverAll replays retained
// history before live messages. SinceSeq, when non-zero, resumes from that
// stream sequence (inclusive) and takes priority over DeliverAll — it is set by
// the SDK on reconnect to resume from last-delivered+1.
type SubscribeInput struct {
	Subject    string `json:"subject"`
	SubID      string `json:"sub_id"`
	DeliverAll bool   `json:"deliver_all"`
	SinceSeq   uint64 `json:"since_seq,omitempty"`
}

// SubscribeOutput confirms the subscription and echoes the delivery subject the
// bus will publish to (the SDK already knows it; this is a defensive check).
type SubscribeOutput struct {
	DeliverSubject string `json:"deliver_subject"`
}

// MessageDelivery is one pushed message frame, published to the subscription's
// delivery subject. It carries the bus-trusted position and clock alongside the
// stamped frame so the SDK delivers the same Message it would from a pull.
type MessageDelivery struct {
	SubID   string     `json:"sub_id"`
	Subject string     `json:"subject"`
	Seq     uint64     `json:"seq"`
	BusTime time.Time  `json:"bus_time"`
	Frame   wire.Frame `json:"frame"`
}

// --- artifact.watch (push-stream over sx.deliver.<id>.<sub_id>) ---

// WatchInput starts a push-stream watch on one artifact. SubID is the
// client-generated ULID naming the delivery subject.
type WatchInput struct {
	Name  string `json:"name"`
	SubID string `json:"sub_id"`
}

// WatchOutput confirms the watch and echoes the delivery subject.
type WatchOutput struct {
	DeliverSubject string `json:"deliver_subject"`
}

// ArtifactDelivery is one pushed artifact change: the current value first, then
// each later write and delete. On a delete, Deleted is true and Record/timestamps
// are empty.
type ArtifactDelivery struct {
	SubID     string          `json:"sub_id"`
	Name      string          `json:"name"`
	Record    json.RawMessage `json:"record,omitempty"`
	Revision  uint64          `json:"revision"`
	CreatedAt string          `json:"createdAt,omitempty"`
	UpdatedAt string          `json:"updatedAt,omitempty"`
	Deleted   bool            `json:"deleted"`
}

// --- subscription.stop ---

// SubscriptionStopInput ends the subscription named by SubID (idempotent: a
// SubID the bus no longer tracks is a success, not an error).
type SubscriptionStopInput struct {
	SubID string `json:"sub_id"`
}

// --- clients.register (issuance) ---

// RegisterInput is the issuance request (ADR-0020). The caller asks the bus to
// mint a NEW identity (it does not name itself — the id is generated by the bus):
// DisplayName is the human label to carry in the credential and record, Kind is
// what the new client is. The authorization (held-identity vs bootstrap/
// enrollment) is the caller's reserved id, not a field here.
type RegisterInput struct {
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
}

// RegisterOutput returns the freshly minted identity: its bus-generated ULID id
// and its NATS credential (JWT+seed). The credential is secret material, so the
// reply rides the caller's own connection (per-client inbox). The caller writes
// the credential to a file and hands it to the new client.
type RegisterOutput struct {
	ID    string `json:"id"`
	Creds string `json:"creds"`
}

// --- clients.retire (decommission) ---

// RetireInput decommissions the identity named by ID for good (operator-only).
// Distinct from a disconnect, which only drops presence to offline.
type RetireInput struct {
	ID string `json:"id"`
}

// --- clients.hello (connect handshake) ---

// HelloInput carries no fields: the caller is identified by the call's subject
// token (its authenticated id).
type HelloInput struct{}

// HelloOutput returns the bus's protocol epoch (the SDK exact-matches it, failing
// loud on mismatch), the bus-stamped server time (the SDK clock-skew-checks
// against it), and the current principal ULID (so a client discovers the
// designation in the same round-trip it confirms its identity — ADR-0030). The
// handshake asserts no presence — online/offline is derived from the connection
// itself.
type HelloOutput struct {
	BusEpoch   int    `json:"bus_epoch"`
	ServerTime string `json:"server_time"`
	Principal  string `json:"principal,omitempty"`
}

// --- clients.heartbeat (liveness signal, TASK-126) ---

// HeartbeatInput carries the client's monotonic beat number; the caller is
// identified by the call's subject token (its authenticated id), and the
// timestamp is the bus's to stamp (the client clock is not trusted for last_seen).
// Seq lets the client correlate the echo it receives back on its delivery path
// (the mode-D push-path check) with the beat it sent.
type HeartbeatInput struct {
	Seq uint64 `json:"seq"`
}

// HeartbeatOutput acknowledges the beat: ServerTime is the bus-stamped last_seen
// it recorded (RFC3339), so the client and the bus agree on the recorded value.
type HeartbeatOutput struct {
	ServerTime string `json:"server_time"`
}

// HeartbeatEcho is the beat the bus pushes back down the caller's delivery path
// (its always-present auto-inbox relay) so the client confirms its own push path
// is delivering — a beat sent but not echoed within the window is a stale push
// (TASK-124 mode-D). Seq echoes HeartbeatInput.Seq for correlation.
type HeartbeatEcho struct {
	Seq uint64 `json:"seq"`
}

// --- principal.get / principal.set (ADR-0030 extension) ---

// PrincipalGetInput carries no fields: the principal is a single bus-wide datum.
type PrincipalGetInput struct{}

// PrincipalGetOutput is the current principal ULID. It is empty when no principal
// is designated (the bus defaults one at bootstrap, so an empty value is only
// seen if a fork or an operator cleared it).
type PrincipalGetOutput struct {
	Principal string `json:"principal"`
}

// PrincipalSetInput points the principal at a client ULID (ADR-0030, ADR-0031).
// Authorization is asymmetric around whether the principal is still unclaimed:
// claiming the bootstrap default is open to the bootstrap tier and a kind=client
// target; re-pointing an established principal is operator-only and needs Force.
// The bus stores the value verbatim on a re-point (the two-way door — a wrong
// value is corrected by another set).
type PrincipalSetInput struct {
	Principal string `json:"principal"`
	// Force authorizes re-pointing an ALREADY-established principal. It is
	// ignored while the principal is still unclaimed (the first claim never needs
	// it). The bus rejects a re-point to a different ULID unless Force is set, so
	// moving operator-equivalence takes intent, not a casual overwrite.
	Force bool `json:"force,omitempty"`
}

// PrincipalSetOutput confirms the new principal.
type PrincipalSetOutput struct {
	Principal string `json:"principal"`
}

// --- principal.watch (push-stream over sx.deliver.<id>.<sub_id>) ---

// PrincipalWatchInput starts a push-stream watch of the principal designation.
// SubID is the client-generated ULID naming the delivery subject.
type PrincipalWatchInput struct {
	SubID string `json:"sub_id"`
}

// PrincipalWatchOutput confirms the watch and echoes the delivery subject.
type PrincipalWatchOutput struct {
	DeliverSubject string `json:"deliver_subject"`
}

// PrincipalDelivery is one pushed principal change: the current value first, then
// each re-designation. Principal is the ULID now in effect.
type PrincipalDelivery struct {
	SubID     string `json:"sub_id"`
	Principal string `json:"principal"`
}

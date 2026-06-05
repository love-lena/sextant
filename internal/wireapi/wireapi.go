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
	OpArtifactDelete   = "artifact.delete"
	OpArtifactWatch    = "artifact.watch"
	OpClientsList      = "clients.list"
)

// OpSubscriptionStop is the internal control op that ends a push-stream
// subscription (message.subscribe / artifact.watch). It is bus plumbing, not one
// of the protocol's operations (it is not in methods.json and has no CLI/MCP
// surface): the SDK calls it from Subscription.Stop / Watch.Stop to tear down the
// server-side relay it started.
const OpSubscriptionStop = "subscription.stop"

// OpClientsRegister and OpClientsDeregister are the connect-handshake ops — the
// write half of the clients directory (clients.list is the read half). A client
// registers itself by calling clients.register, and the bus validates that call
// like any other: it keys the record by the caller's authenticated id (never the
// body), so a client can only ever register *as itself*. They are control ops,
// not user-invoked protocol operations (not in methods.json, no CLI/MCP surface) —
// the SDK issues them automatically on Connect and Close — but they are
// first-class, bus-validated requests, not a side channel. Folding the epoch
// hard-gate into register keeps the handshake one round-trip: register returns
// the bus's epoch (the SDK gates on it) and the bus-stamped connected_at (the SDK
// clock-skew-checks against it).
const (
	OpClientsRegister   = "clients.register"
	OpClientsDeregister = "clients.deregister"
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

// --- clients.list ---

// ClientsListOutput is the clients registry directory.
type ClientsListOutput struct {
	Clients []ClientEntry `json:"clients"`
}

// ClientEntry is one registry record (matches the sx_clients record shape). ID
// is the bus-minted ULID; DisplayName is the human label.
type ClientEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	Epoch       int    `json:"epoch"`
	SDK         string `json:"sdk"`
	ConnectedAt string `json:"connected_at"`
}

// --- message.subscribe (push-stream over sx.deliver.<id>.<sub_id>) ---

// SubscribeInput starts a push-stream subscription. SubID is the
// client-generated ULID naming the delivery subject; DeliverAll replays retained
// history before live messages.
type SubscribeInput struct {
	Subject    string `json:"subject"`
	SubID      string `json:"sub_id"`
	DeliverAll bool   `json:"deliver_all"`
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

// --- clients.register / clients.deregister (connect handshake) ---

// RegisterInput carries the client's self-declared directory fields. The id is
// the call's subject token (the bus stamps it as the record key), not the body,
// so a client cannot register under an identity it did not authenticate as.
type RegisterInput struct {
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	Epoch       int    `json:"epoch"`
	SDK         string `json:"sdk"`
}

// RegisterOutput returns the bus's protocol epoch (the SDK hard-gates on it) and
// the bus-stamped connected_at (the SDK clock-skew-checks against it). The bus
// registers the client only if its epoch matches; an incompatible client still
// gets the epoch back so the SDK can fail loud without ever entering the directory.
type RegisterOutput struct {
	BusEpoch    int    `json:"bus_epoch"`
	ConnectedAt string `json:"connected_at"`
}

// DeregisterInput leaves the directory; the id is the call's subject token.
type DeregisterInput struct{}

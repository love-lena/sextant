// Package wireapi defines the Wire API: the client↔bus call protocol (ADR-0019
// §1). A client invokes an operation by making a NATS request to
// sx.api.<clientID>.<operation> and gets a Response; the bus serves it against
// the backend interface and stamps the frame. This package holds the subject
// scheme, the operation names (mirroring protocol/methods.json), and the
// per-operation request/response shapes shared by the bus and the SDK.
//
// It is internal plumbing: the SDK wraps it, so a client program never imports
// these types. The subject token <clientID> is the call's claimed author; once
// the per-client allow-list credential is in place it is also the authenticated
// identity (the client may publish only under its own sx.api.<id> prefix), which
// is what makes the bus-stamped author unforgeable.
package wireapi

import (
	"encoding/json"
	"strings"

	"github.com/love-lena/sextant/pkg/wire"
)

// APIPrefix is the root of the client→bus call space: sx.api.<clientID>.<op>.
const APIPrefix = "sx.api."

// WildcardSubject is what the bus subscribes to in order to receive every call.
const WildcardSubject = APIPrefix + "*.>"

// DeliverPrefix is the root of the bus→client push space (subscribe/watch
// delivery): sx.deliver.<clientID>.<stream>. Owner-subscribe only.
const DeliverPrefix = "sx.deliver."

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

// CallSubject builds the request subject for clientID invoking op.
func CallSubject(clientID, op string) string { return APIPrefix + clientID + "." + op }

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

// ClientEntry is one registry record (matches the sx_clients record shape).
type ClientEntry struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Epoch       int    `json:"epoch"`
	SDK         string `json:"sdk"`
	ConnectedAt string `json:"connected_at"`
}

// Package wire defines Sextant's wire atom: a JSON frame the bus stamps around a
// typed lexicon record. The record is user space (the client supplies it); the
// frame is bus space (the bus produces it). The frozen wrapper core
// (ID/Author/Kind/Epoch) is the only thing a protocol-epoch bump protects;
// Record is the free-evolution zone.
//
// See ADR-0006 (the wire atom), ADR-0010 (lifecycle & versioning), and ADR-0018
// / ADR-0019 (the bus implements the protocol and stamps the frame).
package wire

import (
	"encoding/json"
	"time"

	"github.com/oklog/ulid/v2"
)

// Frame kinds. Kind discriminates a frame: a message in flight or an artifact at
// rest (frame.json knownValues).
const (
	KindMessage  = "message"
	KindArtifact = "artifact"
)

// Epoch is the protocol epoch the SDK writes and checks. It bumps only on a
// breaking wire change; additive changes never bump it. See ADR-0010.
const Epoch = 1

// SkewTolerance is the default maximum allowed difference between a ULID's
// embedded timestamp and the bus-stamped time. See ADR-0006.
const SkewTolerance = 5 * time.Minute

// Lexicon is the typed content a frame carries: an AT-Protocol lexicon record,
// JSON by convention for now. It is an alias today, which names the concept and
// leaves a seam to attach validation or codegen later without changing the
// Frame.Record type here.
type Lexicon = json.RawMessage

// Frame is the wire atom: the bus-stamped wrapper around a typed record. The
// record is user space (the client supplies it); the frame is bus space (the bus
// produces id, author, and the rest). Kind discriminates it: a message in flight
// or an artifact at rest. See ADR-0018/ADR-0019.
type Frame struct {
	// ID is a ULID; it is also the dedup key. Bus-stamped (ADR-0019). For a
	// message, its embedded timestamp is not a trusted clock (see CheckSkew).
	ID string `json:"id"`
	// Author is the authenticated identity the bus took from the request. Not
	// client-set, and not forgeable by editing the record (ADR-0019).
	Author string `json:"author"`
	// Kind is the frame kind: KindMessage or KindArtifact.
	Kind string `json:"kind"`
	// Epoch is the protocol epoch this frame was written under, checked
	// per-message because durable streams outlive epochs.
	Epoch int `json:"epoch"`
	// Record is the typed content: an AT-Protocol lexicon record, opaque to the
	// bus and always JSON on this wire. Binary is a first-class bytes/blob value
	// within it — inline as {"$bytes": base64} now, native under a later
	// DAG-CBOR encoding, or a blob reference when large (ADR-0016).
	Record Lexicon `json:"record"`

	// The following are bus-stamped and present only when Kind is KindArtifact.

	// Revision is the artifact revision; it advances on each write. Bus-stamped.
	Revision uint64 `json:"revision,omitempty"`
	// CreatedAt is the artifact's creation time (RFC3339). Bus-stamped.
	CreatedAt string `json:"createdAt,omitempty"`
	// UpdatedAt is the time of the latest artifact write (RFC3339). Bus-stamped.
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// New builds a valid message frame for the current epoch: a fresh ULID stamped
// at the current time, KindMessage, and the given author and record. Under
// ADR-0019 the bus owns frame stamping; this is the message constructor used
// until the bus serves the publish operation.
func New(author string, record Lexicon) Frame {
	return Frame{
		ID:     ulid.Make().String(),
		Author: author,
		Kind:   KindMessage,
		Epoch:  Epoch,
		Record: record,
	}
}

// Encode marshals the frame to its JSON wire form.
func Encode(f Frame) ([]byte, error) {
	return json.Marshal(f)
}

// Decode unmarshals a frame from its JSON wire form.
func Decode(b []byte) (Frame, error) {
	var f Frame
	err := json.Unmarshal(b, &f)
	return f, err
}

// Package wire defines Sextant's wire atom: a JSON envelope wrapping a typed
// lexicon record. The frozen wrapper core (ID/Sender/Kind/Epoch) is the only
// thing a protocol-epoch bump protects; Record is the free-evolution zone.
//
// See ADR-0006 (the wire atom) and ADR-0010 (lifecycle & versioning).
package wire

import (
	"encoding/json"
	"time"

	"github.com/oklog/ulid/v2"
)

// KindMessage is the only envelope frame type for now. Kind names the frame,
// leaving room for other frames in a later epoch.
const KindMessage = "message"

// Epoch is the protocol epoch the SDK writes and checks. It bumps only on a
// breaking wire change; additive changes never bump it. See ADR-0010.
const Epoch = 1

// SkewTolerance is the default maximum allowed difference between a ULID's
// embedded timestamp and the bus-stamped time. See ADR-0006.
const SkewTolerance = 5 * time.Minute

// Lexicon is the typed content of a message: an AT-Protocol lexicon record,
// JSON by convention for now. It is an alias today, which names the concept and
// leaves a seam to attach validation or codegen later without changing the
// Envelope.Record type here.
type Lexicon = json.RawMessage

// Envelope is the wire atom: a JSON envelope wrapping a typed record.
type Envelope struct {
	// ID is a ULID set by the sender; it is also the dedup key. The embedded
	// timestamp is sender-set, so it is not a trusted clock (see CheckSkew).
	ID string `json:"id"`
	// Sender is the authenticated identity that published the message.
	Sender string `json:"sender"`
	// Kind is the frame type; KindMessage for now.
	Kind string `json:"kind"`
	// Epoch is the protocol epoch this message was written under, checked
	// per-message because durable streams outlive epochs.
	Epoch int `json:"epoch"`
	// Record is the typed content: an AT-Protocol lexicon record, opaque to
	// the bus and always JSON. Binary lives in an artifact, referenced here.
	Record Lexicon `json:"record"`
}

// New builds a valid envelope for the current epoch: a fresh ULID stamped at
// the current time, KindMessage, and the given sender and record.
func New(sender string, record Lexicon) Envelope {
	return Envelope{
		ID:     ulid.Make().String(),
		Sender: sender,
		Kind:   KindMessage,
		Epoch:  Epoch,
		Record: record,
	}
}

// Encode marshals the envelope to its JSON wire form.
func Encode(e Envelope) ([]byte, error) {
	return json.Marshal(e)
}

// Decode unmarshals an envelope from its JSON wire form.
func Decode(b []byte) (Envelope, error) {
	var e Envelope
	err := json.Unmarshal(b, &e)
	return e, err
}

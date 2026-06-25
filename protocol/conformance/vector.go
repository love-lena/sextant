// Package conformance defines the language-neutral conformance-vector format —
// the contract that makes a client co-equal (ADR-0041). A vector is a recorded
// transcript: given an input, a convention verb (or a frame codec) produces
// exactly these primitive bus operations, in this order. Every language's SDK
// replays the same JSON files and must reproduce the same operations; passing
// the suite for a protocol epoch is what defines a correct client.
//
// This package is deliberately language-neutral plumbing: it parses the vector
// JSON and canonicalizes payloads for comparison. It imports no convention and
// no client — the Go runner that *replays* a vector by invoking a verb lives in
// client land (sdk/conformance), because importing a verb would make the
// protocol depend on a client and break importcheck (ADR-0041). What lives here
// is only what a recorder and a replayer of ANY language need to agree on: the
// on-disk shape and the canonical-JSON rule.
//
// The vectors themselves are JSON under vectors/ next to this package, so they
// are shipped with the protocol and read by every language's suite from one
// well-known location.
package conformance

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Kind names the two vector families. They share the canonical-JSON rule but
// describe different layers: a WireVector pins the frame codec (bytes), an
// OpTranscriptVector pins a convention verb's behaviour (operations).
type Kind string

const (
	// KindWire is a frame-codec vector: a frame and its exact canonical bytes.
	// Each SDK's codec must encode the frame to those bytes and decode the bytes
	// to that frame. Lives under vectors/wire/.
	KindWire Kind = "wire"
	// KindOpTranscript is a convention-behaviour vector: an input and the ordered
	// primitive operations the verb emits. Lives under vectors/<convention>/.
	KindOpTranscript Kind = "op-transcript"
)

// Op is one expected primitive bus operation in a transcript. Op MUST be an
// operation name that appears in protocol/methods.json (artifact.create,
// artifact.update, artifact.get, artifact.list, artifact.delete,
// message.publish, message.read, message.subscribe, …) — the runner asserts
// this. Subject is set for message operations (the msg.* subject); Name is set
// for artifact operations (the artifact name). Payload is the canonical record
// or argument set the operation carries; it is compared as canonical JSON
// (see Canonicalize). ExpectedRev, when present, is the compare-and-set
// revision an artifact.update was issued with.
//
// Only the fields an operation uses are populated; the rest are omitted from
// the JSON. The set of populated fields is itself part of the contract — a
// verb that omits a subject is observably different from one that sets it.
type Op struct {
	Op          string          `json:"op"`
	Subject     string          `json:"subject,omitempty"`
	Name        string          `json:"name,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
	ExpectedRev *uint64         `json:"expectedRev,omitempty"`
}

// OpTranscriptVector is a convention-behaviour vector: replaying Verb with
// Input against a recording client must emit Operations, in order. Epoch pins
// the vector to a protocol epoch — a client that conforms at one epoch need not
// at another. Convention and Verb name the verb to invoke; the Go runner maps
// (Convention, Verb) to a registered Go verb. Input is the domain arguments the
// verb is called with, opaque to this package (the verb decodes it).
type OpTranscriptVector struct {
	Epoch       int             `json:"epoch"`
	Convention  string          `json:"convention"`
	Verb        string          `json:"verb"`
	Description string          `json:"description,omitempty"`
	Input       json.RawMessage `json:"input"`
	Operations  []Op            `json:"operations"`
}

// WireVector is a frame-codec vector: encoding Frame must yield Bytes (hex),
// and decoding Bytes must yield Frame. Epoch pins the codec version. Frame is
// the structured frame (header fields + record), Bytes is the lowercase-hex
// canonical serialization. (TASK-174 consumes these for the TS frame codec;
// this package and the Go runner establish the format and a Go-side sample.)
type WireVector struct {
	Epoch       int             `json:"epoch"`
	Description string          `json:"description,omitempty"`
	Frame       json.RawMessage `json:"frame"`
	Bytes       string          `json:"bytes"`
}

// Validate checks an op-transcript vector is well-formed independent of any
// verb: it has a convention, a verb, an epoch, and every operation names an op.
// It does NOT check op names against methods.json — that parity is a separate
// suite-level assertion (the runner's protocol-surface test), kept here so a
// pure parse stays free of a methods.json read.
func (v OpTranscriptVector) Validate() error {
	if v.Convention == "" {
		return errors.New("conformance: vector has no convention")
	}
	if v.Verb == "" {
		return errors.New("conformance: vector has no verb")
	}
	if v.Epoch == 0 {
		return errors.New("conformance: vector has no epoch (epoch is 1-based; 0 means unset)")
	}
	for i, op := range v.Operations {
		if op.Op == "" {
			return fmt.Errorf("conformance: operation %d has no op name", i)
		}
	}
	return nil
}

// Canonicalize returns the canonical-JSON encoding of raw, the single rule both
// a Go recorder and a TypeScript recorder must reproduce to capture identical
// vectors. The rule, stated for any-language implementers:
//
//   - Object keys are sorted by Unicode code point, ascending, recursively.
//   - No insignificant whitespace: no spaces, no newlines, no indentation.
//   - Strings use Go's encoding/json escaping (the JSON standard): characters
//     U+0000–U+001F, '"' and '\' are escaped; '<', '>', '&' are NOT escaped
//     (HTML escaping is disabled), matching JSON.stringify.
//   - Numbers are normalized to minimal canonical form: an integer-valued
//     number emits its exact integer digits with no fraction, no trailing ".0",
//     no leading zeros, and no leading '+' (so 1.0 → "1"); a large integer
//     beyond float64 precision keeps its exact digits (so 9007199254740993
//     survives, not rounded); a non-integer uses the shortest float round-trip
//     form (so 1.50 → "1.5"), matching JavaScript's JSON.stringify. Transcript
//     payloads are domain records, not arbitrary-precision math.
//   - UTF-8 throughout.
//   - null, true, false, arrays preserve order (arrays are order-sensitive).
//
// The implementation is parse-into-`any` then re-encode with sorted keys: Go's
// json.Marshal sorts map keys, and decoding an object into map[string]any makes
// every object a sorted-on-encode map. A TS implementer reproduces it by
// recursively sorting object keys and JSON.stringify with no spacer.
func Canonicalize(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte("null"), nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // preserve integer/number form rather than coercing to float64
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("conformance: canonicalize parse: %w", err)
	}
	return canonicalEncode(v)
}

// canonicalEncode encodes v with sorted object keys, no HTML escaping, and no
// insignificant whitespace. It walks the value so the sort is recursive
// (json.Marshal already sorts map keys, but encoding through it directly would
// HTML-escape; the explicit walk lets us disable that and keep numbers as
// json.Number).
func canonicalEncode(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // Go's sort.Strings is byte-wise == Unicode code point order for UTF-8
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSONString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	case json.Number:
		buf.WriteString(canonicalNumber(t))
		return nil
	default:
		// Scalars (string, json.Number, bool, nil) re-encode without HTML escaping
		// via a non-escaping encoder, so '<', '>', '&' survive verbatim — matching
		// JSON.stringify and keeping payloads byte-identical across languages.
		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return err
		}
		// Encoder.Encode appends a newline; trim it so scalars carry no whitespace.
		b := buf.Bytes()
		if n := len(b); n > 0 && b[n-1] == '\n' {
			buf.Truncate(n - 1)
		}
		return nil
	}
}

// canonicalNumber normalizes a JSON number to its minimal canonical text — the
// rule a TS implementer reproduces. An integer-valued number keeps its EXACT
// integer digits (so 9007199254740993, beyond float64's precision, survives),
// with a leading '+' and redundant leading zeros stripped; a non-integer
// re-encodes through float64 in shortest round-trip form (strconv 'g', -1),
// which matches JSON.stringify. So 1.0 → "1", 1.50 → "1.5", 1e2 → "100".
//
// The integer fast-path is by string inspection, not float parsing, precisely
// so large integers are not rounded — a transcript carries bus sequences and
// ULapse-style counters that can exceed 2^53.
func canonicalNumber(n json.Number) string {
	s := string(n)
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return strconv.FormatInt(i, 10)
	}
	// A digit-only string longer than int64 (a big integer) is kept verbatim
	// after stripping a leading '+' — re-parsing as float would lose precision.
	if isIntegerLiteral(s) {
		return strings.TrimPrefix(s, "+")
	}
	f, err := n.Float64()
	if err != nil {
		// Unparseable as float (an oversized non-integer); fall back to the literal
		// minus a redundant '+'. Transcript payloads are domain records, not
		// arbitrary-precision math, so this path is not expected in practice.
		return strings.TrimPrefix(s, "+")
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// isIntegerLiteral reports whether s is a base-10 integer with no fraction or
// exponent (optionally a leading sign). It lets canonicalNumber keep an exact
// big integer without a lossy float round-trip.
func isIntegerLiteral(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '+' || s[0] == '-' {
		i = 1
	}
	if i == len(s) {
		return false
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// writeJSONString writes s as a JSON string with HTML escaping disabled, the
// same rule scalars use, so object keys and values share one escaping.
func writeJSONString(buf *bytes.Buffer, s string) error {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	b := buf.Bytes()
	if n := len(b); n > 0 && b[n-1] == '\n' {
		buf.Truncate(n - 1)
	}
	return nil
}

// CanonicalEqual reports whether two raw JSON payloads are equal under the
// canonical rule. It is the comparison the runner uses for each operation's
// payload. A nil/empty payload canonicalizes to null, so two absent payloads
// compare equal.
func CanonicalEqual(a, b json.RawMessage) (bool, error) {
	ca, err := Canonicalize(a)
	if err != nil {
		return false, fmt.Errorf("conformance: canonicalize left: %w", err)
	}
	cb, err := Canonicalize(b)
	if err != nil {
		return false, fmt.Errorf("conformance: canonicalize right: %w", err)
	}
	return bytes.Equal(ca, cb), nil
}

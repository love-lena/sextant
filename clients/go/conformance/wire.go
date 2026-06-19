package conformance

import (
	"encoding/hex"
	"fmt"
	"testing"

	pconf "github.com/love-lena/sextant/protocol/conformance"
	"github.com/love-lena/sextant/protocol/wire"
)

// ReplayWireVectors discovers the wire (frame-codec) vectors under dir/wire and
// asserts the Go frame codec is byte-faithful to each: decoding the vector's
// hex bytes yields the recorded frame, AND the recorded frame re-encodes to a
// payload canonical-equal to the vector's bytes. The second direction is the
// one that pins cross-language parity — every SDK's codec must serialize the
// same frame to the same canonical JSON, so TASK-174's TS codec replays these
// exact files.
//
// Frame bytes are compared canonically (not raw-equal), because the wire form
// is JSON and a vector's bytes are stored canonically; an SDK is free to emit
// keys in any order on the wire as long as the canonical form matches. The
// bytes field is lowercase hex of the canonical JSON.
func ReplayWireVectors(t *testing.T, dir string) {
	t.Helper()
	vectors, err := pconf.LoadWireVectors(dir)
	if err != nil {
		t.Fatalf("load wire vectors: %v", err)
	}
	for _, lv := range vectors {
		lv := lv
		t.Run(relName(dir, lv.Path), func(t *testing.T) {
			raw, err := hex.DecodeString(lv.Vector.Bytes)
			if err != nil {
				t.Fatalf("%s: bytes are not valid hex: %v", lv.Path, err)
			}
			// Decode direction: bytes → frame must match the recorded frame.
			f, err := wire.Decode(raw)
			if err != nil {
				t.Fatalf("%s: decode bytes: %v", lv.Path, err)
			}
			gotFrame, err := wire.Encode(f)
			if err != nil {
				t.Fatalf("%s: re-encode decoded frame: %v", lv.Path, err)
			}
			eq, err := pconf.CanonicalEqual(gotFrame, lv.Vector.Frame)
			if err != nil {
				t.Fatalf("%s: canonicalize: %v", lv.Path, err)
			}
			if !eq {
				cf, _ := pconf.Canonicalize(lv.Vector.Frame)
				cg, _ := pconf.Canonicalize(gotFrame)
				t.Errorf("%s: decoded frame mismatch:\n want %s\n got  %s", lv.Path, cf, cg)
			}
			// Encode direction: frame → canonical bytes must match the vector.
			encoded, err := wire.Encode(f)
			if err != nil {
				t.Fatalf("%s: encode frame: %v", lv.Path, err)
			}
			canon, err := pconf.Canonicalize(encoded)
			if err != nil {
				t.Fatalf("%s: canonicalize encoded: %v", lv.Path, err)
			}
			gotHex := hex.EncodeToString(canon)
			wantHex := hex.EncodeToString(mustCanon(t, lv.Path, raw))
			if gotHex != wantHex {
				t.Errorf("%s: encoded bytes mismatch:\n want %s\n got  %s", lv.Path, wantHex, gotHex)
			}
		})
	}
}

func mustCanon(t *testing.T, path string, raw []byte) []byte {
	t.Helper()
	c, err := pconf.Canonicalize(raw)
	if err != nil {
		t.Fatalf("%s: canonicalize vector bytes: %v", path, err)
	}
	return c
}

// RecordWireVector builds a wire vector from a frame: its canonical JSON, the
// lowercase hex of those bytes, and a description. It is the recording half a
// `-update` path uses to (re)generate a wire sample from the Go codec.
func RecordWireVector(epoch int, description string, f wire.Frame) (pconf.WireVector, error) {
	encoded, err := wire.Encode(f)
	if err != nil {
		return pconf.WireVector{}, fmt.Errorf("conformance: encode frame: %w", err)
	}
	canon, err := pconf.Canonicalize(encoded)
	if err != nil {
		return pconf.WireVector{}, fmt.Errorf("conformance: canonicalize frame: %w", err)
	}
	return pconf.WireVector{
		Epoch:       epoch,
		Description: description,
		Frame:       canon,
		Bytes:       hex.EncodeToString(canon),
	}, nil
}

// WriteWireVector serializes a wire vector to path in the human-reviewable
// pretty form (the same on-disk shape WriteVector uses for op-transcripts).
func WriteWireVector(path string, v pconf.WireVector) error {
	return writeJSONVector(path, v)
}

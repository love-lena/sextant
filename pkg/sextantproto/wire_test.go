package sextantproto

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// wireManifest mirrors the structure the generator emits to
// schemas/wire.json. It is the single machine-readable source the TS
// codegen and the CI schema-compat gate both consume, so the envelope
// kinds, address kinds, frame kinds, proto version, and WireEpoch are
// generated, not hand-synced.
type wireManifest struct {
	ProtoVersion string   `json:"proto_version"`
	WireEpoch    int      `json:"wire_epoch"`
	Kinds        []string `json:"kinds"`
	AddressKinds []string `json:"address_kinds"`
	FrameKinds   []string `json:"frame_kinds"`
}

// TestWireManifestMatchesGoConstants asserts the committed wire.json
// manifest is in sync with the Go source of truth. The generator walks
// the Go constants; this guards against a hand edit to wire.json drifting
// from the constants (and against forgetting to regenerate).
func TestWireManifestMatchesGoConstants(t *testing.T) {
	path := filepath.Join("schemas", "wire.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (run `go generate ./pkg/sextantproto/...`): %v", path, err)
	}
	var m wireManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	if m.ProtoVersion != ProtoVersion {
		t.Errorf("wire.json proto_version = %q, want %q", m.ProtoVersion, ProtoVersion)
	}
	if m.WireEpoch != WireEpoch {
		t.Errorf("wire.json wire_epoch = %d, want %d", m.WireEpoch, WireEpoch)
	}

	wantKinds := make([]string, 0, len(AllKinds()))
	for _, k := range AllKinds() {
		wantKinds = append(wantKinds, string(k))
	}
	if !equalStrings(m.Kinds, wantKinds) {
		t.Errorf("wire.json kinds = %v, want %v", m.Kinds, wantKinds)
	}

	wantAddr := make([]string, 0, len(AllAddressKinds()))
	for _, k := range AllAddressKinds() {
		wantAddr = append(wantAddr, string(k))
	}
	if !equalStrings(m.AddressKinds, wantAddr) {
		t.Errorf("wire.json address_kinds = %v, want %v", m.AddressKinds, wantAddr)
	}

	wantFrame := make([]string, 0, len(AllFrameKinds()))
	for _, k := range AllFrameKinds() {
		wantFrame = append(wantFrame, string(k))
	}
	if !equalStrings(m.FrameKinds, wantFrame) {
		t.Errorf("wire.json frame_kinds = %v, want %v", m.FrameKinds, wantFrame)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

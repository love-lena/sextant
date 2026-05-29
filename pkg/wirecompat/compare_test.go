package wirecompat

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSchemas materializes a map of filename→content into a temp dir and
// returns the dir. Used to build old/new schema snapshots for Compare.
func writeSchemas(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

const baseWire = `{
  "proto_version": "0.5.0",
  "wire_epoch": 1,
  "kinds": ["agent_frame", "lifecycle"],
  "address_kinds": ["agent", "operator"],
  "frame_kinds": ["assistant_text", "tool_call"]
}`

// schemaWith builds a minimal one-object schema file with the given
// properties and required list.
func envelopeSchema(props, required string) string {
	return `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$defs": {
    "Envelope": {
      "type": "object",
      "properties": {` + props + `},
      "required": [` + required + `]
    }
  }
}`
}

func TestCompareIdenticalIsNotBreaking(t *testing.T) {
	files := map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}`, `"id"`),
	}
	oldDir := writeSchemas(t, files)
	newDir := writeSchemas(t, files)

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) != 0 {
		t.Fatalf("identical schemas reported breaking: %v", res.Breaking)
	}
}

func TestRemovedFieldIsBreaking(t *testing.T) {
	oldDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}, "ts": {"type": "string"}`, `"id"`),
	})
	newDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}`, `"id"`),
	})

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) == 0 {
		t.Fatal("removed field should be breaking")
	}
}

func TestAddedOptionalFieldIsNotBreaking(t *testing.T) {
	oldDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}`, `"id"`),
	})
	newDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}, "ts": {"type": "string"}`, `"id"`),
	})

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) != 0 {
		t.Fatalf("added optional field should not be breaking: %v", res.Breaking)
	}
}

func TestTypeChangeIsBreaking(t *testing.T) {
	oldDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}`, `"id"`),
	})
	newDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "integer"}`, `"id"`),
	})

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) == 0 {
		t.Fatal("type change should be breaking")
	}
}

func TestOptionalToRequiredIsBreaking(t *testing.T) {
	oldDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}, "ts": {"type": "string"}`, `"id"`),
	})
	newDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}, "ts": {"type": "string"}`, `"id", "ts"`),
	})

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) == 0 {
		t.Fatal("optional→required should be breaking")
	}
}

func TestRemovedEnumValueIsBreaking(t *testing.T) {
	newWire := `{
  "proto_version": "0.5.0",
  "wire_epoch": 1,
  "kinds": ["agent_frame"],
  "address_kinds": ["agent", "operator"],
  "frame_kinds": ["assistant_text", "tool_call"]
}`
	oldDir := writeSchemas(t, map[string]string{"wire.json": baseWire})
	newDir := writeSchemas(t, map[string]string{"wire.json": newWire})

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) == 0 {
		t.Fatal("removed enum (kind) value should be breaking")
	}
}

func TestAddedEnumValueIsNotBreaking(t *testing.T) {
	newWire := `{
  "proto_version": "0.5.0",
  "wire_epoch": 1,
  "kinds": ["agent_frame", "lifecycle", "audit"],
  "address_kinds": ["agent", "operator"],
  "frame_kinds": ["assistant_text", "tool_call"]
}`
	oldDir := writeSchemas(t, map[string]string{"wire.json": baseWire})
	newDir := writeSchemas(t, map[string]string{"wire.json": newWire})

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) != 0 {
		t.Fatalf("added enum value should not be breaking: %v", res.Breaking)
	}
}

func TestRemovedSchemaFileIsBreaking(t *testing.T) {
	oldDir := writeSchemas(t, map[string]string{
		"wire.json":      baseWire,
		"envelope.json":  envelopeSchema(`"id": {"type": "string"}`, `"id"`),
		"heartbeat.json": envelopeSchema(`"x": {"type": "string"}`, ``),
	})
	newDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}`, `"id"`),
	})

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) == 0 {
		t.Fatal("removed schema file should be breaking")
	}
}

// Epoch helpers tie the breaking verdict to the gate decision.

func TestNeedsBumpFailsWhenBreakingWithoutBump(t *testing.T) {
	res := Result{
		Breaking: []string{"envelope.json: removed property ts"},
		OldEpoch: 1,
		NewEpoch: 1,
	}
	if !res.NeedsBump() {
		t.Fatal("breaking change without epoch bump must require a bump")
	}
}

func TestNeedsBumpPassesWhenBreakingWithBump(t *testing.T) {
	res := Result{
		Breaking: []string{"envelope.json: removed property ts"},
		OldEpoch: 1,
		NewEpoch: 2,
	}
	if res.NeedsBump() {
		t.Fatal("breaking change WITH an epoch bump must pass the gate")
	}
}

func TestNeedsBumpPassesWhenNoBreaking(t *testing.T) {
	res := Result{Breaking: nil, OldEpoch: 1, NewEpoch: 1}
	if res.NeedsBump() {
		t.Fatal("no breaking change must pass the gate")
	}
}

// TestMissingBaselineWireIsTolerated covers the bootstrap PR that first
// introduces wire.json: the baseline snapshot has no wire.json, so the old
// epoch defaults to 0 and no enum-removal is detectable. Compare must not
// error.
func TestMissingBaselineWireIsTolerated(t *testing.T) {
	oldDir := writeSchemas(t, map[string]string{
		// No wire.json in the baseline.
		"envelope.json": envelopeSchema(`"id": {"type": "string"}`, `"id"`),
	})
	newDir := writeSchemas(t, map[string]string{
		"wire.json":     baseWire,
		"envelope.json": envelopeSchema(`"id": {"type": "string"}`, `"id"`),
	})

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare with absent baseline wire.json: %v", err)
	}
	if res.OldEpoch != 0 {
		t.Errorf("absent baseline wire.json should yield OldEpoch 0, got %d", res.OldEpoch)
	}
	if len(res.Breaking) != 0 {
		t.Errorf("no structural change should be non-breaking: %v", res.Breaking)
	}
}

package wirecompat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// committedSchemasDir is the real on-disk schema snapshot, relative to
// this package.
const committedSchemasDir = "../sextantproto/schemas"

// copyTree shallow-copies every regular file from src into a fresh temp
// dir and returns the dir. Subdirectories are skipped (the schema dir is
// flat).
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), raw, 0o644); err != nil {
			t.Fatalf("write %s: %v", e.Name(), err)
		}
	}
	return dst
}

// TestRealSchemasSelfComparisonIsClean is the live baseline: the committed
// schemas compared against themselves report no breaking change. If this
// fails, the committed schemas are internally inconsistent.
func TestRealSchemasSelfComparisonIsClean(t *testing.T) {
	res, err := Compare(committedSchemasDir, committedSchemasDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if res.NeedsBump() {
		t.Fatalf("committed schemas vs themselves require a bump: %v", res.Breaking)
	}
	if len(res.Breaking) != 0 {
		t.Fatalf("committed schemas vs themselves report breaking: %v", res.Breaking)
	}
}

// TestRealSchemasGateFailsOnBreakingNoBump is the end-to-end proof the C1
// ticket requires: a PR with a breaking schema change but NO epoch bump
// must fail. We copy the real schemas, remove a required property from the
// envelope, leave wire_epoch untouched, and assert NeedsBump() is true.
func TestRealSchemasGateFailsOnBreakingNoBump(t *testing.T) {
	oldDir := copyTree(t, committedSchemasDir)
	newDir := copyTree(t, committedSchemasDir)

	// Breaking change: drop trace_id from the Envelope.
	envPath := filepath.Join(newDir, "envelope.json")
	raw, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read envelope.json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse envelope.json: %v", err)
	}
	defs := doc["$defs"].(map[string]any)
	env := defs["Envelope"].(map[string]any)
	props := env["properties"].(map[string]any)
	delete(props, "trace_id")
	if req, ok := env["required"].([]any); ok {
		filtered := req[:0]
		for _, r := range req {
			if r != "trace_id" {
				filtered = append(filtered, r)
			}
		}
		env["required"] = filtered
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal envelope.json: %v", err)
	}
	if err := os.WriteFile(envPath, out, 0o644); err != nil {
		t.Fatalf("write envelope.json: %v", err)
	}

	res, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(res.Breaking) == 0 {
		t.Fatal("removing a required envelope property should be breaking")
	}
	if !res.NeedsBump() {
		t.Fatalf("breaking change without epoch bump must require a bump; breaking=%v old=%d new=%d",
			res.Breaking, res.OldEpoch, res.NewEpoch)
	}

	// Now bump the epoch in the new snapshot and confirm the gate passes.
	wirePath := filepath.Join(newDir, "wire.json")
	wraw, err := os.ReadFile(wirePath)
	if err != nil {
		t.Fatalf("read wire.json: %v", err)
	}
	w := map[string]any{}
	if err := json.Unmarshal(wraw, &w); err != nil {
		t.Fatalf("parse wire.json: %v", err)
	}
	w["wire_epoch"] = int(w["wire_epoch"].(float64)) + 1
	wout, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		t.Fatalf("marshal wire.json: %v", err)
	}
	if err := os.WriteFile(wirePath, wout, 0o644); err != nil {
		t.Fatalf("write wire.json: %v", err)
	}

	res2, err := Compare(oldDir, newDir)
	if err != nil {
		t.Fatalf("Compare (post-bump): %v", err)
	}
	if res2.NeedsBump() {
		t.Fatalf("breaking change WITH an epoch bump must pass; breaking=%v old=%d new=%d",
			res2.Breaking, res2.OldEpoch, res2.NewEpoch)
	}
}

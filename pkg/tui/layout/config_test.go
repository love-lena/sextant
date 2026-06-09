package layout_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/love-lena/sextant/pkg/tui/layout"
	"github.com/love-lena/sextant/pkg/tui/theme"
)

// TestConfigRoundTrip is AC#1: a config saved then loaded comes back equal. It
// covers the active preset, the hidden set, and the theme.
func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.json")
	want := layout.Config{
		Version: layout.ConfigVersion,
		Preset:  layout.PresetSplit,
		Hidden:  []string{"artifacts"},
		Theme:   theme.VariantLight,
	}
	if err := layout.SaveConfig(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := layout.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

// TestConfigPlacementsRoundTrip is AC#3: the free-placement seam survives a
// round-trip intact, proving a future free-placement file is preserved by
// today's preset-mode reader/writer without a rewrite of the schema.
func TestConfigPlacementsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.json")
	want := layout.Config{
		Version: layout.ConfigVersion,
		Preset:  layout.PresetCockpit,
		Theme:   theme.VariantDark,
		Placements: []layout.Placement{
			{PaneID: "clients", X: 0, Y: 0, W: 30, H: 100},
			{PaneID: "topics", X: 30, Y: 0, W: 70, H: 60},
		},
	}
	if err := layout.SaveConfig(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := layout.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got.Placements, want.Placements) {
		t.Errorf("placements not preserved:\n got %+v\nwant %+v", got.Placements, want.Placements)
	}
}

// TestLoadMissingFallsBackToDefault is AC#1's robustness clause: a missing file
// is not an error — it yields DefaultConfig cleanly, no panic.
func TestLoadMissingFallsBackToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	got, err := layout.LoadConfig(path)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if !reflect.DeepEqual(got, layout.DefaultConfig()) {
		t.Errorf("missing file = %+v, want DefaultConfig %+v", got, layout.DefaultConfig())
	}
}

// TestLoadOldConfigFillsDefaults: an older file missing the version/preset/theme
// fields loads cleanly with those defaulted, never panicking — the version seam
// lets an old file migrate forward.
func TestLoadOldConfigFillsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	// A pre-versioning file: just a hidden list, no version/preset/theme.
	if err := os.WriteFile(path, []byte(`{"hidden":["topics"]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := layout.LoadConfig(path)
	if err != nil {
		t.Fatalf("load old: %v", err)
	}
	if got.Version != layout.ConfigVersion {
		t.Errorf("version not defaulted: %d", got.Version)
	}
	if got.Preset != layout.PresetCockpit {
		t.Errorf("preset not defaulted: %q", got.Preset)
	}
	if got.Theme != theme.VariantDark {
		t.Errorf("theme not defaulted: %q", got.Theme)
	}
	if len(got.Hidden) != 1 || got.Hidden[0] != "topics" {
		t.Errorf("hidden lost: %v", got.Hidden)
	}
}

// TestLoadMalformedIsLoudError: a corrupt file fails loudly rather than silently
// falling back to defaults (a corrupt config is a real problem, not noise to
// paper over).
func TestLoadMalformedIsLoudError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := layout.LoadConfig(path); err == nil {
		t.Fatal("malformed config should be a loud error, got nil")
	}
}

// TestSaveStampsVersion: SaveConfig always writes the current ConfigVersion even
// if the in-memory config had a stale one.
func TestSaveStampsVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "layout.json")
	if err := layout.SaveConfig(path, layout.Config{Version: 0, Preset: layout.PresetSplit, Theme: theme.VariantDark}); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var c layout.Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Version != layout.ConfigVersion {
		t.Errorf("version not stamped: %d", c.Version)
	}
}

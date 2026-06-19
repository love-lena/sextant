package layout_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/layout"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/theme"
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
	if got.Theme != theme.VariantAuto {
		t.Errorf("theme not defaulted to auto: %q", got.Theme)
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

// TestAutoThemeSurvivesUntouchedSession is the auto-stays-auto regression
// guard: the dash's real round-trip is LoadConfig → layout.New (with the
// HOST-RESOLVED concrete theme — the probe ran at the composition root) →
// operate → Model.Config() → SaveConfig on exit. A persisted "auto" must come
// back out as "auto" after a session that never touched the theme — were the
// Model to emit the variant the probe resolved to (here dark), auto would be a
// one-launch choice and detection would never run again.
func TestAutoThemeSurvivesUntouchedSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.json")
	seed := layout.DefaultConfig()
	seed.Theme = theme.VariantAuto
	if err := layout.SaveConfig(path, seed); err != nil {
		t.Fatalf("save seed: %v", err)
	}
	cfg, err := layout.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// theme.Dark() stands in for the host's per-launch resolution of auto.
	m := layout.New(theme.Dark(), theme.DefaultKeymap(), cfg,
		newMock("clients", "Clients"), newMock("topics", "Topics"), newMock("artifacts", "Artifacts"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(key("p")) // operate (cycle preset) — but never touch the theme

	if m.Theme().Variant != theme.VariantDark {
		t.Fatalf("render theme = %q, want the host-resolved dark", m.Theme().Variant)
	}
	if err := layout.SaveConfig(path, m.Config()); err != nil {
		t.Fatalf("save on exit: %v", err)
	}
	got, err := layout.LoadConfig(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Theme != theme.VariantAuto {
		t.Errorf("persisted theme = %q after an untouched session, want auto", got.Theme)
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

// TestModelPlacementsRoundTrip is TestConfigPlacementsRoundTrip's production-
// path sibling: the dash's real round-trip is LoadConfig → layout.New →
// operate → Model.Config() → SaveConfig on exit, so the free-placement seam
// must survive the MODEL, not just the file codec. A populated Placements
// rides through construction, a pane toggle, and a preset cycle, and lands
// back in the saved file byte-for-byte — an older (preset-mode) binary never
// silently deletes a newer file's free-placement data on exit.
func TestModelPlacementsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.json")
	want := []layout.Placement{
		{PaneID: "clients", X: 0, Y: 0, W: 30, H: 100},
		{PaneID: "topics", X: 30, Y: 0, W: 70, H: 60},
	}
	seed := layout.DefaultConfig()
	seed.Placements = want
	if err := layout.SaveConfig(path, seed); err != nil {
		t.Fatalf("save seed: %v", err)
	}
	cfg, err := layout.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// The real path: build the cockpit from the loaded config and operate it —
	// a pane toggle (via the options menu) and a preset cycle both rebuild
	// state the snapshot reads back.
	m := layout.New(theme.Dark(), theme.DefaultKeymap(), cfg,
		newMock("clients", "Clients"), newMock("topics", "Topics"), newMock("artifacts", "Artifacts"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = m.Update(key("o"))     // open options
	m, _ = m.Update(key("enter")) // toggle clients off
	m, _ = m.Update(key("esc"))   // close menu
	m, _ = m.Update(key("p"))     // cycle preset

	out := m.Config()
	if !reflect.DeepEqual(out.Placements, want) {
		t.Fatalf("Model.Config dropped the placements:\n got %+v\nwant %+v", out.Placements, want)
	}
	// And the operated state still snapshots (the seam rides along, it does not
	// replace the live fields).
	if !contains(out.Hidden, "clients") {
		t.Errorf("hidden set lost through the snapshot: %v", out.Hidden)
	}

	// Exit: save the snapshot and reload — the file still carries the seam.
	if err := layout.SaveConfig(path, out); err != nil {
		t.Fatalf("save on exit: %v", err)
	}
	got, err := layout.LoadConfig(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reflect.DeepEqual(got.Placements, want) {
		t.Errorf("placements not preserved through the Model round-trip:\n got %+v\nwant %+v", got.Placements, want)
	}
}

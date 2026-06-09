package layout

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant/pkg/tui/theme"
)

// ConfigVersion is the schema version stamped into a saved Config. It is the
// seam that lets the format grow: a reader checks Version and migrates an older
// file forward rather than failing. Bump it when the schema changes in a way an
// old reader could not understand.
const ConfigVersion = 1

// Config is the persisted layout choice (ADR-0023): which preset is active,
// which panes the operator has hidden, and the theme variant. It is serialised
// as JSON and round-trips through SaveConfig/LoadConfig so the cockpit reopens
// the way the operator left it.
//
// The schema carries an explicit seam for the deferred free-placement mode
// (ADR-0023: "shaped so they can arrive later without a rewrite"). Today the
// dash is preset-mode only: Preset names a built-in arrangement and Hidden lists
// the panes toggled off. The Placements field is the seam — when it is empty the
// layout is in preset-mode (the default); when a future build writes explicit
// per-pane rectangles into it, the same file describes a free-placement layout
// without changing the surrounding shape. Today's layout ignores a populated
// Placements (preset-mode wins) but preserves it across a load/save round-trip,
// so an older binary never silently drops a newer file's free-placement data.
type Config struct {
	// Version is the schema version (ConfigVersion). LoadConfig tolerates an older
	// or absent version by falling back to defaults for the fields it cannot read,
	// never by failing.
	Version int `json:"version"`

	// Preset is the name of the active built-in arrangement (e.g. "cockpit").
	// An unknown name falls back to the default preset when applied.
	Preset string `json:"preset"`

	// Hidden lists the ids of panes the operator has toggled off. A pane not in
	// this list (and present in the host's surface set) is visible.
	Hidden []string `json:"hidden,omitempty"`

	// Theme is the chosen theme variant ("light" or "dark"). An empty or unknown
	// value resolves to the dash default when applied.
	Theme theme.Variant `json:"theme"`

	// Placements is the free-placement seam (deferred, ADR-0023). Empty in
	// preset-mode (today's only mode); a future build writes explicit rectangles
	// here. Preset-mode is the default whenever Placements is empty. The layout
	// preserves a populated Placements across a round-trip but does not yet render
	// from it.
	Placements []Placement `json:"placements,omitempty"`
}

// Placement is one pane's explicit rectangle in the deferred free-placement
// mode (ADR-0023). It is the unit the Placements seam carries. The coordinates
// are a normalised grid the future free-placement engine will interpret; today
// the field exists only so the schema and the round-trip already accommodate it.
type Placement struct {
	// PaneID is the id of the surface this placement positions.
	PaneID string `json:"paneId"`
	// X and Y are the placement's top-left origin; W and H its size. Their units
	// are reserved for the future free-placement engine (today they are stored
	// verbatim and not interpreted).
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// DefaultConfig returns the cockpit default: the cockpit preset, nothing hidden,
// the dark theme, preset-mode (no placements). It is what a fresh dash starts
// from and what LoadConfig falls back to when no file exists.
func DefaultConfig() Config {
	return Config{
		Version: ConfigVersion,
		Preset:  PresetCockpit,
		Theme:   theme.VariantDark,
	}
}

// LoadConfig reads a Config from a JSON file at path. A missing file is not an
// error: it returns DefaultConfig and a nil error, so a first run starts clean.
// A malformed file is a loud error (fail-loud: a corrupt config is a real
// problem, not something to paper over with defaults). The path is a parameter,
// not resolved here — the host (7.5) chooses the real location; this package is
// internal-free and never reaches for $SEXTANT_HOME.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read layout config %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse layout config %s: %w", path, err)
	}
	// Fill fields an older or partial file left empty, so a loaded config is always
	// usable without a panic. An unknown preset/theme is left as-is and resolved
	// (to the default) at apply time, where the surface set is known.
	if c.Version == 0 {
		c.Version = ConfigVersion
	}
	if c.Preset == "" {
		c.Preset = PresetCockpit
	}
	if c.Theme == "" {
		c.Theme = theme.VariantDark
	}
	return c, nil
}

// SaveConfig writes a Config to path as indented JSON, creating the parent
// directory if it is missing. It stamps the current ConfigVersion so a reader
// can tell what wrote the file. The host calls it on change or on quit; the
// path is the host's choice.
func SaveConfig(path string, c Config) error {
	c.Version = ConfigVersion
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode layout config: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create layout config dir %s: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write layout config %s: %w", path, err)
	}
	return nil
}

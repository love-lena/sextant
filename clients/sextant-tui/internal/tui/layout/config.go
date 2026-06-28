package layout

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
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

	// Theme is the operator's theme choice: a concrete variant ("light" or
	// "dark"), or "auto" — re-detect the terminal background at every launch.
	// Auto persists AS auto: this package only stores the choice; the host
	// resolves it (the terminal probe is the composition root's job) before
	// constructing the layout. An empty or unknown value resolves to auto when
	// applied.
	Theme theme.Variant `json:"theme"`
}

// DefaultConfig returns the cockpit default: the cockpit preset, nothing hidden,
// the auto theme (detect the terminal background at every launch). It is what a
// fresh dash starts from and what LoadConfig falls back to when no file exists —
// so a first run on a light terminal opens light.
func DefaultConfig() Config {
	return Config{
		Version: ConfigVersion,
		Preset:  PresetCockpit,
		Theme:   theme.VariantAuto,
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
		c.Theme = theme.VariantAuto
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

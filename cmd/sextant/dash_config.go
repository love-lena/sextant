// dash_config.go owns the TOML schema + loader for `sextant dash`.
//
// The schema is intentionally narrow: a `[dash]` table with a
// `panes` array, each entry carrying an `id` + `command`. Operators
// can override the embedded default by dropping a file at
// `~/.config/sextant/config.toml`; the loader prefers the override
// when present and falls back to the embedded default otherwise.
//
// Wire shape (verbatim from `cmd/sextant/dash-default-config.toml`):
//
//	[[dash.panes]]
//	id = "agents"
//	command = "agents list"
//
// Validation rules:
//
//   - Each pane MUST have a non-empty id and command.
//   - Pane ids MUST be unique across the layout — the dash uses id as
//     the focus-cycle key and the BubbleZone region label, so a
//     duplicate would silently overwrite the registration.
//
// Malformed TOML and validation failures both return a structured
// error so cobra surfaces them via the standard sextant error
// banner.
package main

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// defaultDashConfigTOML is the in-binary fallback. The file lives
// next to this Go file so contributors can read + copy it without
// spelunking through the binary. Embedded via go:embed.
//
//go:embed dash-default-config.toml
var defaultDashConfigTOML string

// dashConfig is the top-level TOML envelope. We keep the wire shape
// nested under `dash` rather than promoting `panes` to root so a
// future expansion (`[dash.theme]`, `[dash.keymap]`, ...) doesn't
// require a breaking config migration.
type dashConfig struct {
	Dash dashSection `toml:"dash"`
}

// dashSection holds the [dash] table from the TOML file. Only
// `panes` is supported today; future fields land here.
type dashSection struct {
	Panes []paneConfig `toml:"panes"`
}

// paneConfig is one entry in `[[dash.panes]]`. ID is the stable
// focus-key + zone label; Command is the CLI invocation we route to
// the registered Component (split on whitespace, matched against
// component.Meta.Command).
type paneConfig struct {
	ID      string `toml:"id"`
	Command string `toml:"command"`
}

// loadDashConfig returns the operator's resolved dash layout.
// overrideDir, when non-empty, is the directory we look in for
// `config.toml`; empty means "use ~/.config/sextant". A missing
// override file is NOT an error — we fall back to the embedded
// default. Malformed TOML and validation failures are surfaced
// verbatim so cobra prints them via the error banner.
func loadDashConfig(overrideDir string) (dashConfig, error) {
	if overrideDir == "" {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			overrideDir = filepath.Join(home, ".config", "sextant")
		}
	}
	if overrideDir != "" {
		overridePath := filepath.Join(overrideDir, "config.toml")
		// gosec G304: overridePath is derived from $HOME / --config-dir,
		// not from untrusted input — the operator owns this path.
		if data, err := os.ReadFile(overridePath); err == nil { //nolint:gosec
			return parseDashConfig(data, overridePath)
		} else if !errors.Is(err, os.ErrNotExist) {
			// Surface non-NotExist read errors (permission denied,
			// etc.) rather than silently falling back — an unreadable
			// override file is an operator-visible misconfiguration.
			return dashConfig{}, fmt.Errorf("read %s: %w", overridePath, err)
		}
	}
	return parseDashConfig([]byte(defaultDashConfigTOML), "<embedded default>")
}

// parseDashConfig unmarshals data into a dashConfig and validates
// it. source is the path (or `<embedded default>` placeholder) we
// include in error messages so operators can find the file.
func parseDashConfig(data []byte, source string) (dashConfig, error) {
	var cfg dashConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return dashConfig{}, fmt.Errorf("parse %s: %w", source, err)
	}
	if err := validateDashConfig(cfg); err != nil {
		return dashConfig{}, fmt.Errorf("%s: %w", source, err)
	}
	return cfg, nil
}

// validateDashConfig enforces the schema invariants documented in
// the package comment: non-empty panes list, non-empty id +
// command per pane, unique ids.
func validateDashConfig(cfg dashConfig) error {
	if len(cfg.Dash.Panes) == 0 {
		return errors.New("dash: at least one pane is required")
	}
	seen := make(map[string]struct{}, len(cfg.Dash.Panes))
	for i, p := range cfg.Dash.Panes {
		if p.ID == "" {
			return fmt.Errorf("dash.panes[%d]: id is required", i)
		}
		if p.Command == "" {
			return fmt.Errorf("dash.panes[%d] (id=%q): command is required", i, p.ID)
		}
		if _, dup := seen[p.ID]; dup {
			return fmt.Errorf("dash.panes[%d]: duplicate id %q", i, p.ID)
		}
		seen[p.ID] = struct{}{}
	}
	return nil
}

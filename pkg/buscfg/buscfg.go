// Package buscfg is the small, optional config file `sextant up` reads from the
// bus store dir — beside bus.json (conninfo) — so settings that must survive a
// launcher restart (brew services / launchd) can be set on disk rather than
// passed as a start-time flag. launchd regenerates the plist and does not
// inherit the shell env, so neither a flag nor an env var reaches the bus across
// a `brew services restart`; a file the bus reads on `up` is the path that does.
//
// Scope (v0.5.1): leaf-listen only. The file carries the same loopback host:port
// `--leaf-listen` accepts; the leaf trust model (ADR-0038) is unchanged — this
// is only where the listen address comes from. Default-off is preserved: an
// absent (or empty-leaf-listen) config is byte-identical to passing nothing.
package buscfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultFile is the config filename, written under the bus store dir (beside
// bus.json). It is optional — `sextant up` runs identically when it is absent.
const DefaultFile = "config.json"

// Path is the config file's location under the store dir.
func Path(storeDir string) string { return filepath.Join(storeDir, DefaultFile) }

// Config is the on-disk bus config. Fields are all optional; a zero Config is
// the default-off case (no leaf listener). Not secret — the leaf listen address
// is the same loopback host:port the operator would pass to --leaf-listen.
type Config struct {
	// LeafListen, when set, is the hub-side leaf listener address (loopback
	// host:port). Empty means OFF, identical to omitting --leaf-listen. The
	// address is validated by the bus when the listener is wired (a malformed or
	// non-loopback address fails `up` loudly there), not here.
	LeafListen string `json:"leaf-listen,omitempty"`
}

// Load reads the config file at path. A missing file is NOT an error — it is the
// default-off case, returning a zero Config. A present-but-unreadable or
// malformed file IS an error: `up` must fail loud rather than silently start
// without the configured leaf (or with a wrong one).
func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil // absent config == default-off, not a failure
	}
	if err != nil {
		return c, fmt.Errorf("buscfg: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("buscfg: parse %s: %w", path, err)
	}
	return c, nil
}

// Save writes the config file at path, creating the parent store dir if needed.
// The file is owner+group+world readable like bus.json — the leaf listen address
// is not secret (the link credential beside it is the secret material).
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("buscfg: create dir: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("buscfg: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("buscfg: write %s: %w", path, err)
	}
	return nil
}

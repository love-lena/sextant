// Package buscfg is the small, optional config file `sextant up` reads from the
// bus store dir — beside bus.json (conninfo) — so settings that must survive a
// launcher restart (brew services / launchd) can be set on disk rather than
// passed as a start-time flag. launchd regenerates the plist and does not
// inherit the shell env, so neither a flag nor an env var reaches the bus across
// a `brew services restart`; a file the bus reads on `up` is the path that does.
//
// Scope: the listen knobs that must survive a launcher restart — leaf-listen
// (ADR-0038), the pinned port, and websocket-listen (ADR-0044). Each carries the
// same loopback host:port its flag accepts; this file is only where the value
// comes from, the trust models are unchanged. Default-off is preserved: an absent
// (or empty-valued) config is byte-identical to passing nothing.
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

	// WebSocketListen, when set, is the bus WebSocket listener address (loopback
	// host:port). Empty means OFF, byte-identical to omitting it. The browser dash
	// reaches the bus over this listener as a co-equal TS client (ADR-0044). The
	// address is validated by the bus when the listener is wired (a non-loopback
	// address fails `up` loudly there), not here. Like leaf-listen, it sits behind
	// the operator's secure transport (loopback) — native wss TLS is a follow-up.
	WebSocketListen string `json:"websocket-listen,omitempty"`

	// Port, when non-zero, pins the bus client listen port deterministically. It
	// is the brew-services equivalent of `--port` (launchd inherits neither flag
	// nor env across a restart): a recorded value the bus reads on `up`, so the
	// port survives `brew services restart` and clients stay reachable instead of
	// re-resolving after a silent random rebind. Zero means OFF — the bus reuses
	// the recorded port from bus.json when free, else binds random (ADR-0025). An
	// unavailable pinned port fails `up` loudly (the bus probes it), never silently.
	Port int `json:"port,omitempty"`
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

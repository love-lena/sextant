// Package conninfo is the small discovery file `sextant up` writes so a client
// knows the bus URL. Credentials are not here: under JWT auth each client has
// its own creds file, issued by the bus via `sextant clients register`
// (ADR-0020 — the bus is the sole minter; the signing keys never leave it).
package conninfo

import (
	"encoding/json"
	"fmt"
	"os"
)

// DefaultFile is the discovery filename, written under the bus store dir.
const DefaultFile = "bus.json"

// Info is how a client finds the bus. The URL is not secret.
type Info struct {
	URL string `json:"url"`
	// WSURL is the bus WebSocket listener URL (ws://host:port), present only when
	// the bus was started with a WebSocket listener (ADR-0044). It is how the
	// browser dash discovers where to dial: the dash reads it here and hands it to
	// the page at credential-mint time. Empty/omitted means no WebSocket listener —
	// byte-identical to before for every non-dash client, which ignores it.
	WSURL string `json:"wsURL,omitempty"`
}

// Write writes the discovery file at path.
func Write(path string, i Info) error {
	b, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return fmt.Errorf("conninfo: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("conninfo: write %s: %w", path, err)
	}
	return nil
}

// Read reads the discovery file at path.
func Read(path string) (Info, error) {
	var i Info
	b, err := os.ReadFile(path)
	if err != nil {
		return i, fmt.Errorf("conninfo: read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &i); err != nil {
		return i, fmt.Errorf("conninfo: parse %s: %w", path, err)
	}
	return i, nil
}

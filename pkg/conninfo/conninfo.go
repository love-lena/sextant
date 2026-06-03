// Package conninfo is the small discovery file `sextant up` writes so a client
// knows the bus URL. Credentials are not here: under JWT auth each client has
// its own creds file, minted by `sextant token` (ADR-0012).
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

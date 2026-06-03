// Package conninfo is the small connection-info file `sextant up` writes so a
// client (the SDK) can find the bus and its client-tier credentials.
//
// Writing the client password to disk is a v1 placeholder for the deferred
// credential-minting design (ADR-0012 open item); the file is mode 0600.
package conninfo

import (
	"encoding/json"
	"fmt"
	"os"
)

// DefaultFile is the conn-info filename, written under the bus store dir.
const DefaultFile = "bus.json"

// Info is how a client reaches the bus.
type Info struct {
	URL            string `json:"url"`
	ClientUser     string `json:"client_user"`
	ClientPassword string `json:"client_password"`
}

// Write writes the conn-info file at path with mode 0600.
func Write(path string, i Info) error {
	b, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return fmt.Errorf("conninfo: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("conninfo: write %s: %w", path, err)
	}
	return nil
}

// Read reads the conn-info file at path.
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

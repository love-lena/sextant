package attest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant/clients/go/apps/internal/seqcursor"
)

// Identity is the per-session record of WHICH bus identity the MCP server
// actually connected as. The server is the single source of truth for identity
// (it alone resolves/mints, ADR-0029); the attest hook is a SEPARATE process and
// must NOT re-derive identity independently — a re-derivation can diverge from
// the server (an in-session context_use switch the hook can't observe; a
// concurrent first-turn mint; a session-id source mismatch). Instead the server
// records what it connected as here, and the hook FOLLOWS it: it loads these
// exact creds and connects co-identity, so the hook scans the SAME worker's own
// DM subject the server is on — lockstep by construction, in every case (pinned
// $SEXTANT_CREDS/$SEXTANT_CONTEXT, a context_use switch, or a per-session mint).
//
// On disk it is one file per session id under the writable, persistent
// CLAUDE_PLUGIN_DATA (never the read-only plugin root), keyed on the stable
// CLAUDE_CODE_SESSION_ID — the same dir/keying convention as the delivery cursor
// (cursor.go), so identity and cursor resume together.
type Identity struct {
	// Creds is the credentials file the server connected with — the hook opens
	// the SAME file so it is co-identity with the server.
	Creds string `json:"creds"`
	// URL is the bus URL the server resolved (may be "" when discovery supplied
	// it; the hook then falls back to its own discovery path).
	URL string `json:"url"`
	// ID is the bus-stamped author ULID the server connected as — recorded for
	// diagnostics and as a cross-check (the hook re-reads c.ID() after connect).
	ID string `json:"id"`
}

// identityFile returns the identity path for a session under the plugin data
// dir. It mirrors cursorFile's keying so the two files sit side by side.
func identityFile(dataDir, sessionID string) string {
	name := sessionID
	if name == "" {
		name = "no-session"
	}
	return filepath.Join(dataDir, "attest-identity", seqcursor.Sanitize(name)+".json")
}

// SaveIdentity records the server's connected identity for a session. It is
// called by the MCP server on every (re)connect — including the reconnect a
// context_use switch forces — so the file always reflects the live identity. The
// write is atomic (write-temp-then-rename) so a crash mid-write can never leave a
// half-written file the hook would mis-read. A missing/blank dataDir is a no-op
// error the caller logs but never fails the connect over (the hook degrades).
func SaveIdentity(dataDir, sessionID string, id Identity) error {
	if dataDir == "" {
		return errors.New("attest: no CLAUDE_PLUGIN_DATA; cannot persist identity")
	}
	path := identityFile(dataDir, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("attest: create identity dir: %w", err)
	}
	b, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return fmt.Errorf("attest: marshal identity: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("attest: write identity: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("attest: rename identity: %w", err)
	}
	return nil
}

// ErrNoIdentity reports that no identity file exists for the session yet — the
// server has not connected (e.g. turn 1, before the first tool call). The hook
// treats this as a graceful degrade (exit 0, no additionalContext): turn 1 has
// no inbound messages to attest anyway.
var ErrNoIdentity = errors.New("attest: no identity file for this session yet")

// LoadIdentity reads the identity the server recorded for sessionID under
// dataDir. A missing file returns ErrNoIdentity (the degrade signal). A corrupt
// file is a hard error — unlike the cursor, the hook cannot guess an identity, so
// it degrades rather than connect as the wrong actor.
func LoadIdentity(dataDir, sessionID string) (Identity, error) {
	path := identityFile(dataDir, sessionID)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Identity{}, ErrNoIdentity
	}
	if err != nil {
		return Identity{}, fmt.Errorf("attest: read identity %s: %w", path, err)
	}
	var id Identity
	if err := json.Unmarshal(b, &id); err != nil {
		return Identity{}, fmt.Errorf("attest: decode identity %s: %w", path, err)
	}
	if id.Creds == "" {
		return Identity{}, fmt.Errorf("attest: identity %s has no creds path", path)
	}
	return id, nil
}

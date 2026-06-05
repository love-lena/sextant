package sextant

import (
	"context"
	"fmt"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
)

// ClientInfo is one entry in the clients registry: a client that is connected
// right now. The registry is a presence-only, self-maintained directory — each
// client writes its own entry on Connect and removes it on Close (ADR-0004,
// ADR-0008). "Listed" therefore means "registered and has not cleanly left": a
// client that crashes without Close leaves a stale entry until read-time
// liveness and stale-entry reaping land (TASK-20). There is no heartbeat in M1.
type ClientInfo struct {
	// ID is the client's verified identity — the bus-minted ULID in its
	// credential, which is both its registry key and its frame author. The bus
	// sources it from the registry key (the authoritative locator).
	ID string
	// DisplayName is the human-readable label minted with the credential
	// (`sextant token <display-name>`). Unique by convention, not by the bus.
	DisplayName string
	// Kind is what the client is (e.g. "harness", "coordinator"), self-declared
	// at connect via Options.Kind.
	Kind string
	// Epoch is the protocol epoch the client connected under.
	Epoch int
	// SDK is the SDK version that wrote the entry.
	SDK string
	// ConnectedAt is when the client registered, by its own UTC clock. (The
	// bus-authoritative stamp is what TASK-20 liveness will age against; this
	// self-reported time is the lean M1 field.)
	ConnectedAt time.Time
}

// registryRecord is a client's on-the-wire entry in the registry (the JSON
// stored under its id in sx_clients). It is written by register (the connect
// handshake, see client.go); ClientInfo is its public, parsed view.
type registryRecord struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
	Epoch       int    `json:"epoch"`
	SDK         string `json:"sdk"`
	ConnectedAt string `json:"connected_at"`
}

// checkRecordKey enforces the registry's identity invariant: a record's
// self-reported id must equal the key it is filed under. The key is the
// authoritative identity (what register writes under, and the identity the bus
// authenticated the connection as — ADR-0012); the body id duplicates it. We
// keep that duplicate field in the schema, but never trust it to diverge: a
// mismatch is corruption, rejected on write so the SDK never files one (the bus
// likewise sources the id from the key when it lists).
func checkRecordKey(recordID, key string) error {
	if recordID != key {
		return fmt.Errorf("registry record id %q does not match its key %q", recordID, key)
	}
	return nil
}

// ListClients returns the registry directory: every client connected right now,
// sorted by id, via the clients.list operation (the bus reads the registry and
// sources each id from its authoritative key). The directory is presence-only —
// an entry means the client registered and has not cleanly left (see ClientInfo).
// An empty directory is an empty slice, not an error.
func (c *Client) ListClients(ctx context.Context) ([]ClientInfo, error) {
	var out wireapi.ClientsListOutput
	if err := c.call(ctx, wireapi.OpClientsList, struct{}{}, &out); err != nil {
		return nil, err
	}
	infos := make([]ClientInfo, 0, len(out.Clients))
	for _, e := range out.Clients {
		t, err := time.Parse(time.RFC3339, e.ConnectedAt)
		if err != nil {
			continue // the bus owns these records; skip one with a bad timestamp
		}
		infos = append(infos, ClientInfo{
			ID:          e.ID,
			DisplayName: e.DisplayName,
			Kind:        e.Kind,
			Epoch:       e.Epoch,
			SDK:         e.SDK,
			ConnectedAt: t,
		})
	}
	return infos, nil
}

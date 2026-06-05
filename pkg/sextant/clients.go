package sextant

import (
	"context"
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

// The registry record shape (the JSON stored under each id in sx_clients) is now
// bus-owned: the bus writes it on clients.register and the SDK reads it back via
// clients.list as a wireapi.ClientEntry. The id↔key identity invariant the SDK
// used to guard is now structural — the bus keys every record under the call's
// authenticated subject token, so the body id cannot diverge from the key.

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

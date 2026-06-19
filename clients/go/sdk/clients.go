package sextant

import (
	"context"
	"time"

	"github.com/love-lena/sextant/protocol/wireapi"
)

// ClientInfo is one entry in the clients directory (ADR-0020): a bus-issued
// identity and whether it is connected right now. The directory is a durable
// store of issued identities joined with live presence — an identity is listed
// whether it is online or offline, from the moment it is issued
// (`sextant clients register`) until it is retired (`sextant clients retire`).
// Disconnecting does not remove it; it only flips Presence to offline.
type ClientInfo struct {
	// ID is the client's verified identity — the bus-minted ULID in its
	// credential, which is both its registry key and its frame author. The bus
	// sources it from the registry key (the authoritative locator).
	ID string
	// DisplayName is the human-readable label minted with the credential. Unique
	// by convention, not by the bus.
	DisplayName string
	// Kind is what the client is (e.g. "worker", "reviewer"), declared at issuance.
	Kind string
	// Epoch is the protocol epoch the identity was issued under.
	Epoch int
	// Online is the bus-derived presence: true iff an authenticated connection for
	// this identity exists right now. The bus computes it from the live connection
	// table, not from any stored field (ADR-0020) — there is no heartbeat.
	Online bool
	// IssuedAt is when the bus minted the identity (its UTC clock).
	IssuedAt time.Time
}

// ListClients returns the clients directory: every issued identity — online and
// offline — sorted by id, via the clients.list operation. The bus reads the
// durable records and stamps each with a presence derived from the live
// connection (ADR-0020). An empty directory is an empty slice, not an error.
func (c *Client) ListClients(ctx context.Context) ([]ClientInfo, error) {
	return listClients(ctx, c.call)
}

// Register asks the bus to mint a NEW child identity over THIS client's own
// connection — mint-on-behalf (ADR-0033). Unlike Issuer.Register (held-identity
// or bootstrap authority), this is authorized only when the calling client is a
// registered dispatcher (KindDispatcher); the bus forces every minted identity to
// kind=agent. A reference dispatcher uses it to stand up its children with its
// own authority and no operator credential. The returned creds are secret
// material (they ride this client's per-client inbox) — write them to a file and
// hand them to the child.
func (c *Client) Register(ctx context.Context, displayName, kind string) (IssuedClient, error) {
	var out wireapi.RegisterOutput
	if err := c.call(ctx, wireapi.OpClientsRegister, wireapi.RegisterInput{
		DisplayName: displayName,
		Kind:        kind,
	}, &out); err != nil {
		return IssuedClient{}, err
	}
	return IssuedClient{ID: out.ID, Creds: out.Creds}, nil
}

// listClients is the shared implementation behind Client.ListClients and
// Issuer.ListClients — both make the same clients.list call, differing only in
// which connection (and thus which call subject) carries it.
func listClients(ctx context.Context, call callFunc) ([]ClientInfo, error) {
	var out wireapi.ClientsListOutput
	if err := call(ctx, wireapi.OpClientsList, struct{}{}, &out); err != nil {
		return nil, err
	}
	infos := make([]ClientInfo, 0, len(out.Clients))
	for _, e := range out.Clients {
		t, err := time.Parse(time.RFC3339, e.IssuedAt)
		if err != nil {
			continue // the bus owns these records; skip one with a bad timestamp
		}
		infos = append(infos, ClientInfo{
			ID:          e.ID,
			DisplayName: e.DisplayName,
			Kind:        e.Kind,
			Epoch:       e.Epoch,
			Online:      e.Presence == wireapi.PresenceOnline,
			IssuedAt:    t,
		})
	}
	return infos, nil
}

// callFunc is the signature of the SDK's Wire API call method, shared by Client
// and Issuer so directory reads have one implementation.
type callFunc func(ctx context.Context, op string, in, out any) error

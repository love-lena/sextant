package sextant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
	"github.com/nats-io/nats.go/jetstream"
)

// ClientInfo is one entry in the clients registry: a client that is connected
// right now. The registry is a presence-only, self-maintained directory — each
// client writes its own entry on Connect and removes it on Close (ADR-0004,
// ADR-0008). "Listed" therefore means "registered and has not cleanly left": a
// client that crashes without Close leaves a stale entry until read-time
// liveness and stale-entry reaping land (TASK-20). There is no heartbeat in M1.
type ClientInfo struct {
	// ID is the client's verified identity — its credential's name, which is
	// both its registry key and its envelope sender. ListClients sources it from
	// the registry key (the authoritative locator), not the record body.
	ID string
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
// handshake, see client.go) and read back by ListClients; ClientInfo is its
// public, parsed view.
type registryRecord struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Epoch       int    `json:"epoch"`
	SDK         string `json:"sdk"`
	ConnectedAt string `json:"connected_at"`
}

// checkRecordKey enforces the registry's identity invariant: a record's
// self-reported id must equal the key it is filed under. The key is the
// authoritative identity (what register writes under, and the name the bus
// authenticated the connection as — ADR-0012); the body id duplicates it. We
// keep that duplicate field in the schema, but never trust it to diverge: a
// mismatch is corruption, rejected on write (so the SDK never files one) and on
// read (so a foreign or corrupt one never surfaces).
func checkRecordKey(recordID, key string) error {
	if recordID != key {
		return fmt.Errorf("registry record id %q does not match its key %q", recordID, key)
	}
	return nil
}

// info parses a stored record into its public view. ID is taken from the key
// (the authoritative locator), the body id is only checked against it, and a
// connected_at that isn't the RFC3339 the SDK writes fails loud rather than
// being coerced.
func (r registryRecord) info(key string) (ClientInfo, error) {
	if err := checkRecordKey(r.ID, key); err != nil {
		return ClientInfo{}, err
	}
	t, err := time.Parse(time.RFC3339, r.ConnectedAt)
	if err != nil {
		return ClientInfo{}, fmt.Errorf("bad connected_at %q: %w", r.ConnectedAt, err)
	}
	return ClientInfo{ID: key, Kind: r.Kind, Epoch: r.Epoch, SDK: r.SDK, ConnectedAt: t}, nil
}

// ListClients returns the registry directory: every client connected right now,
// sorted by id. The directory is presence-only — an entry means the client
// registered and has not cleanly left (see ClientInfo). An empty directory is
// an empty slice, not an error.
func (c *Client) ListClients(ctx context.Context) ([]ClientInfo, error) {
	clients, err := c.js.KeyValue(ctx, sx.BucketClients)
	if err != nil {
		return nil, fmt.Errorf("sextant: open %s: %w", sx.BucketClients, err)
	}
	keys, err := clients.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil // empty directory — no clients connected
	}
	if err != nil {
		return nil, fmt.Errorf("sextant: list clients: %w", err)
	}
	out := make([]ClientInfo, 0, len(keys))
	for _, k := range keys {
		e, err := clients.Get(ctx, k)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue // left between the key listing and this read; presence is point-in-time
		}
		if err != nil {
			return nil, fmt.Errorf("sextant: read client %q: %w", k, err)
		}
		var rec registryRecord
		if err := json.Unmarshal(e.Value(), &rec); err != nil {
			return nil, fmt.Errorf("sextant: decode client %q: %w", k, err)
		}
		info, err := rec.info(k)
		if err != nil {
			return nil, fmt.Errorf("sextant: decode client %q: %w", k, err)
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

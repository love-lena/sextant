package sextant

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/protocol/wire"
)

// dialKind issues a client with a specific kind (kind is an issuance-time
// attribute, ADR-0020) and connects it.
func dialKind(t *testing.T, b *bus.Bus, name, kind string) *Client {
	t.Helper()
	creds, _, err := b.MintClient(t.Context(), name, kind)
	if err != nil {
		t.Fatalf("MintClient(%s): %v", name, err)
	}
	path := writeCreds(t, creds)
	c, err := Connect(t.Context(), Options{URL: b.ClientURL(), CredsPath: path, Logf: func(string, ...any) {}})
	if err != nil {
		t.Fatalf("Connect(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestListClients(t *testing.T) {
	b := startBus(t)
	// Names are chosen so the expected sort order is [alpha, beta]; kind is set at
	// issuance, so beta is minted as a coordinator.
	alpha := dialClient(t, b, "c-alpha") // credsPath mints it with kind "test"
	dialKind(t, b, "c-beta", "coordinator")

	got, err := alpha.ListClients(t.Context())
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListClients returned %d clients, want 2: %+v", len(got), got)
	}
	byName := make(map[string]ClientInfo, len(got))
	for _, ci := range got {
		byName[ci.DisplayName] = ci
	}
	alphaInfo, ok := byName["c-alpha"]
	if !ok {
		t.Fatalf("c-alpha not in directory: %+v", got)
	}
	if !alphaInfo.Online {
		t.Errorf("connected alpha should be online: %+v", alphaInfo)
	}

	// beta's record carries the kind it was issued with.
	bt, ok := byName["c-beta"]
	if !ok {
		t.Fatalf("c-beta not in directory: %+v", got)
	}
	if bt.Kind != "coordinator" {
		t.Errorf("beta kind = %q, want coordinator", bt.Kind)
	}
	if bt.Epoch != wire.Epoch {
		t.Errorf("beta epoch = %d, want %d", bt.Epoch, wire.Epoch)
	}
	if bt.IssuedAt.IsZero() || time.Since(bt.IssuedAt) > time.Minute {
		t.Errorf("beta issued_at = %v, want a recent non-zero time", bt.IssuedAt)
	}
	// IDs are bus-minted ULIDs: non-empty and distinct.
	if alphaInfo.ID == "" || alphaInfo.ID == bt.ID {
		t.Errorf("ids should be distinct ULIDs: %q, %q", alphaInfo.ID, bt.ID)
	}
}

// TestListClientsReflectsPresence proves the ADR-0020 directory: a client that
// Closes stays listed (the identity is durable) but flips to offline — it is not
// removed. Removal is retire, not disconnect.
func TestListClientsReflectsPresence(t *testing.T) {
	b := startBus(t)
	alpha := dialClient(t, b, "c-alpha")
	beta := dialClient(t, b, "c-beta")
	betaID := beta.ID()

	waitPresence(t, alpha, betaID, true)
	if got, err := alpha.ListClients(t.Context()); err != nil || len(got) != 2 {
		t.Fatalf("before leave: got %d (err %v), want 2", len(got), err)
	}
	if err := beta.Close(); err != nil {
		t.Fatalf("beta.Close: %v", err)
	}
	waitPresence(t, alpha, betaID, false) // offline, but still listed

	got, err := alpha.ListClients(t.Context())
	if err != nil {
		t.Fatalf("ListClients after leave: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("after beta closed both identities should remain listed: %+v", got)
	}
}

// The empty-directory and corrupt-record paths for ListClients are exercised by
// TestListClientsEmptyDirectory / TestListClientsSkipsCorruptRecords in
// package bus_test (pkg/bus/sdk_integration_test.go): they need to seed/delete
// registry records the bus owns, which the operator seams there provide without a
// production test surface. See docs/conventions/test-features.md.

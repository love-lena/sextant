package sextant

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/wire"
)

// dialOpts dials a client with caller-supplied Options (URL, creds, and a quiet
// logger are filled in), so registry-record fields beyond the id — like Kind —
// can be exercised.
func dialOpts(t *testing.T, b *bus.Bus, id string, opts Options) *Client {
	t.Helper()
	opts.URL = b.ClientURL()
	opts.CredsPath = credsPath(t, b, id)
	opts.Logf = func(string, ...any) {}
	c, err := Connect(t.Context(), opts)
	if err != nil {
		t.Fatalf("Connect(%s): %v", id, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestListClients(t *testing.T) {
	b := startBus(t)
	// alpha is a plain client (default kind); beta declares a kind. Names are
	// chosen so the expected sort order is [alpha, beta].
	alpha := dialClient(t, b, "c-alpha")
	dialOpts(t, b, "c-beta", Options{Kind: "coordinator"})

	got, err := alpha.ListClients(t.Context())
	if err != nil {
		t.Fatalf("ListClients: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListClients returned %d clients, want 2: %+v", len(got), got)
	}
	// The primary id is a bus-minted ULID; look clients up by display_name.
	byName := make(map[string]ClientInfo, len(got))
	for _, ci := range got {
		byName[ci.DisplayName] = ci
	}
	alphaInfo, ok := byName["c-alpha"]
	if !ok {
		t.Fatalf("c-alpha not in directory: %+v", got)
	}
	// The directory includes the caller itself (alpha), with the default kind.
	if alphaInfo.Kind != "client" {
		t.Errorf("alpha kind = %q, want default %q", alphaInfo.Kind, "client")
	}

	// beta's record carries the fields it registered with.
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
	if bt.SDK != sdkVersion {
		t.Errorf("beta sdk = %q, want %q", bt.SDK, sdkVersion)
	}
	if bt.ConnectedAt.IsZero() || time.Since(bt.ConnectedAt) > time.Minute {
		t.Errorf("beta connected_at = %v, want a recent non-zero time", bt.ConnectedAt)
	}
	// IDs are bus-minted ULIDs: non-empty and distinct.
	if alphaInfo.ID == "" || alphaInfo.ID == bt.ID {
		t.Errorf("ids should be distinct ULIDs: %q, %q", alphaInfo.ID, bt.ID)
	}
}

// TestListClientsReflectsDeregister proves "listed = registered and hasn't
// cleanly left": a client that Closes drops out of the directory (deletes are
// filtered, not surfaced as ghost entries).
func TestListClientsReflectsDeregister(t *testing.T) {
	b := startBus(t)
	alpha := dialClient(t, b, "c-alpha")
	beta := dialClient(t, b, "c-beta")

	if got, err := alpha.ListClients(t.Context()); err != nil || len(got) != 2 {
		t.Fatalf("before leave: got %d (err %v), want 2", len(got), err)
	}
	if err := beta.Close(); err != nil {
		t.Fatalf("beta.Close: %v", err)
	}
	got, err := alpha.ListClients(t.Context())
	if err != nil {
		t.Fatalf("ListClients after leave: %v", err)
	}
	if len(got) != 1 || got[0].DisplayName != "c-alpha" {
		t.Fatalf("after beta left: %+v, want only c-alpha", got)
	}
}

// The empty-directory and corrupt-record paths for ListClients are exercised by
// TestListClientsEmptyDirectory / TestListClientsSkipsCorruptRecords in
// package bus_test (pkg/bus/sdk_integration_test.go): they need to seed/delete
// registry records the bus owns, which the operator seams there provide without a
// production test surface. See docs/conventions/test-features.md.

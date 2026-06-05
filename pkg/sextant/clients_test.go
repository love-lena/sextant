package sextant

import (
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/sx"
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

// TestListClientsEmptyDirectory covers the empty-bucket path: NATS KV returns a
// no-keys sentinel rather than an empty list, and ListClients must translate
// that to an empty slice (not an error). We force it by deleting the caller's
// own entry out from under it via a raw KV handle.
func TestListClientsEmptyDirectory(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "c-solo")

	kv, err := inspectJS(t, b).KeyValue(readCtx(t), sx.BucketClients)
	if err != nil {
		t.Fatal(err)
	}
	if err := kv.Delete(readCtx(t), c.ID()); err != nil {
		t.Fatalf("delete sole entry: %v", err)
	}
	got, err := c.ListClients(t.Context())
	if err != nil {
		t.Fatalf("ListClients on empty directory should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty directory returned %d clients: %+v", len(got), got)
	}
}

// TestCheckRecordKey covers the registry identity invariant both paths share:
// the body id must equal the key. register enforces it on write and info on
// read, so testing the helper directly is the deterministic coverage (a
// divergent write can't be produced through the public API).
func TestCheckRecordKey(t *testing.T) {
	if err := checkRecordKey("c-alpha", "c-alpha"); err != nil {
		t.Errorf("matching id/key should pass, got %v", err)
	}
	if err := checkRecordKey("c-impostor", "c-alpha"); err == nil {
		t.Error("a body id that diverges from its key must be rejected")
	}
}

// TestListClientsFailsLoudOnCorruptRecord pins the fail-loud contract: a record
// that isn't the schema the SDK writes — invalid JSON, a non-RFC3339
// connected_at, or a self-reported id that disagrees with its registry key —
// makes the whole call error rather than being silently skipped or coerced. The
// id/key check is the identity guard: the key is authoritative, so a body
// claiming a different id is corruption, not a rename.
func TestListClientsFailsLoudOnCorruptRecord(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "c-real")
	kv, err := inspectJS(t, b).KeyValue(readCtx(t), sx.BucketClients)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, key, value string
	}{
		{"invalid json", "c-badjson", "not json at all"},
		{"bad connected_at", "c-badtime", `{"id":"c-badtime","kind":"x","epoch":1,"sdk":"y","connected_at":"not-a-time"}`},
		{"id disagrees with key", "c-badid", `{"id":"c-impostor","kind":"x","epoch":1,"sdk":"y","connected_at":"2026-06-03T00:00:00Z"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := kv.Put(readCtx(t), tc.key, []byte(tc.value)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = kv.Delete(readCtx(t), tc.key) })
			if _, err := c.ListClients(t.Context()); err == nil {
				t.Errorf("expected ListClients to fail loud on a %s record", tc.name)
			}
		})
	}
}

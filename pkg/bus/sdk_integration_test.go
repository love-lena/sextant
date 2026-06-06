package bus_test

// SDK↔bus integration tests that need to set up bus-internal state a client
// cannot create for itself (a different epoch, a hand-seeded or corrupt registry
// record, a raw frame that bypasses stamping) and then assert the SDK's fail-loud
// and quarantine behaviour against it.
//
// They live here, in the bus package's *external* test package, rather than in
// pkg/sextant, for two reasons that together avoid any build tag or production
// test-seam (see docs/conventions/test-features.md, rung 3):
//   - package bus_test can reach the bus's unexported backend through the
//     re-exports in export_test.go (compiled only into this test binary); and
//   - an external test package may import packages that import the package under
//     test, so it can import pkg/sextant to drive the real SDK even though
//     pkg/sextant imports pkg/bus.
// Housing them with the bus is also honest: they poke internals the bus owns.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/oklog/ulid/v2"
)

func startBus(t *testing.T) *bus.Bus {
	t.Helper()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	return b
}

func readCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// credsPath mints a fresh per-client credential (display_name name) and writes it
// to a temp file, returning the path.
func credsPath(t *testing.T, b *bus.Bus, name string) string {
	t.Helper()
	creds, _, err := b.MintClient(t.Context(), name, "test")
	if err != nil {
		t.Fatalf("MintClient(%s): %v", name, err)
	}
	path := filepath.Join(t.TempDir(), "creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// dialClient mints a credential and connects an SDK client with it.
func dialClient(t *testing.T, b *bus.Bus, name string) *sextant.Client {
	t.Helper()
	c, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: credsPath(t, b, name),
		Logf:      func(string, ...any) {}, // quiet in tests
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestEpochMismatchFailsLoud(t *testing.T) {
	b := startBus(t)
	// Move the bus epoch to something the client won't match. Under the allow-list
	// a client cannot write sx_meta — only the bus can — so this is an operator
	// write seam, which is also the honest shape of an epoch bump in production.
	if err := b.SetEpoch(readCtx(t), 999); err != nil {
		t.Fatal(err)
	}
	_, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: credsPath(t, b, "agent-epoch"),
		Logf:      func(string, ...any) {},
	})
	if err == nil {
		t.Fatal("expected connect to fail loud on epoch mismatch")
	}
	ee, ok := errors.AsType[*wire.EpochError](err)
	if !ok {
		t.Fatalf("expected a *wire.EpochError, got: %v", err)
	}
	if ee.Got != wire.Epoch || ee.Want != 999 {
		t.Errorf("epoch error fields = %+v", ee)
	}
}

// TestListClientsEmptyDirectory covers the empty-bucket path: the backend returns
// a no-keys sentinel rather than an empty list, and ListClients must translate
// that to an empty slice (not an error). We force it by deleting the caller's own
// entry out from under it via the operator seam (a client cannot touch the
// registry directly).
func TestListClientsEmptyDirectory(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "c-solo")

	if err := b.DeleteClientRecord(readCtx(t), c.ID()); err != nil {
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

// TestListClientsSkipsCorruptRecords: clients.list is served by the bus, which
// reads the whole registry on every client's behalf, so a single corrupt record
// skips quietly rather than failing the listing for everyone. The well-formed
// caller is still returned; the corrupt keys are not. We seed the corrupt records
// via the operator seam — a client can't write the registry directly, so this
// stands in for a record some other writer (or an older schema) left behind.
func TestListClientsSkipsCorruptRecords(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "c-real")
	corrupt := map[string]string{
		"c-badjson": "not json at all",
		"c-badtime": `{"id":"c-badtime","kind":"x","epoch":1,"issued_at":"not-a-time"}`,
	}
	for key, value := range corrupt {
		if err := b.SeedClientRecord(readCtx(t), key, []byte(value)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := c.ListClients(t.Context())
	if err != nil {
		t.Fatalf("ListClients should skip corrupt records, not error: %v", err)
	}
	sawReal := false
	for _, ci := range got {
		if _, bad := corrupt[ci.ID]; bad {
			t.Errorf("corrupt record %q should have been skipped, got %+v", ci.ID, ci)
		}
		if ci.DisplayName == "c-real" {
			sawReal = true
		}
	}
	if !sawReal {
		t.Errorf("the well-formed caller c-real should still be listed: %+v", got)
	}
}

// TestSkewQuarantine injects a frame whose ULID time is far in the past
// (bypassing the bus's stamping, via the operator seam) and verifies the receiver
// quarantines it while still delivering a well-formed message — the SDK re-checks
// the clock on consume, so a frame the bus would never stamp (here operator-
// injected; in the field, replayed pre-skew history) cannot slip through.
func TestSkewQuarantine(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "skew-rx")
	ctx := t.Context()
	subj := sx.TopicSubject("skew")

	// A stale frame: ULID timestamp 10 minutes in the past (> 5m tolerance).
	staleID := ulid.MustNew(ulid.Timestamp(time.Now().Add(-10*time.Minute)), ulid.DefaultEntropy()).String()
	stale := wire.Frame{ID: staleID, Author: "rogue", Kind: wire.KindMessage, Epoch: wire.Epoch, Record: json.RawMessage(`{"stale":true}`)}
	staleBytes, _ := wire.Encode(stale)
	if _, err := b.InjectMessage(ctx, subj, staleBytes); err != nil {
		t.Fatalf("inject stale: %v", err)
	}
	// A good frame, published normally.
	if err := c.Publish(ctx, subj, json.RawMessage(`{"good":true}`)); err != nil {
		t.Fatalf("Publish good: %v", err)
	}

	got := make(chan sextant.Message, 4)
	sub, err := c.Subscribe(ctx, subj, func(m sextant.Message) { got <- m }, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop()

	select {
	case m := <-got:
		if m.Frame.ID == staleID {
			t.Fatal("stale (skewed) message was delivered; should have been quarantined")
		}
		if string(m.Frame.Record) != `{"good":true}` {
			t.Errorf("unexpected delivered record: %s", m.Frame.Record)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive the good message")
	}
}

// TestQuarantinesInvalidFrames injects (raw, via the operator seam, bypassing the
// bus's stamping) a wrong-epoch frame and a structurally-malformed one, and
// verifies the receiver delivers only the well-formed message. Clients can no
// longer place a non-conforming frame — the allow-list routes every write through
// the bus — but defense-in-depth still re-checks the wire contract on consume:
// retained history can predate an epoch bump, and a backend is not infallible.
func TestQuarantinesInvalidFrames(t *testing.T) {
	b := startBus(t)
	c := dialClient(t, b, "quar-rx")
	ctx := t.Context()
	subj := sx.TopicSubject("quar")

	// Wrong epoch (otherwise well-formed).
	wrongEpoch := wire.New("rogue", json.RawMessage(`{"epoch":"wrong"}`))
	wrongEpoch.Epoch = wire.Epoch + 1
	weBytes, _ := wire.Encode(wrongEpoch)
	if _, err := b.InjectMessage(ctx, subj, weBytes); err != nil {
		t.Fatalf("inject wrong-epoch: %v", err)
	}
	// Structurally malformed: empty author (Validate rejects it).
	bad := wire.New("", json.RawMessage(`{"bad":true}`))
	badBytes, _ := wire.Encode(bad)
	if _, err := b.InjectMessage(ctx, subj, badBytes); err != nil {
		t.Fatalf("inject malformed: %v", err)
	}
	// A good message.
	if err := c.Publish(ctx, subj, json.RawMessage(`{"good":true}`)); err != nil {
		t.Fatalf("Publish good: %v", err)
	}

	got := make(chan sextant.Message, 8)
	sub, err := c.Subscribe(ctx, subj, func(m sextant.Message) { got <- m }, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Stop()

	select {
	case m := <-got:
		if string(m.Frame.Record) != `{"good":true}` {
			t.Errorf("delivered a quarantined message: record=%s epoch=%d author=%q",
				m.Frame.Record, m.Frame.Epoch, m.Frame.Author)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive the good message")
	}
	// Nothing else should arrive — both bad frames were quarantined.
	select {
	case m := <-got:
		t.Errorf("unexpected second delivery (should have been quarantined): %+v", m.Frame)
	case <-time.After(300 * time.Millisecond):
	}
}

package bus_test

// AC#6 (TASK-25, ADR-0033): mint-on-behalf, inverted. Any registered client may
// mint children with its own authority (no operator credential) — EXCEPT a
// spawned worker, which the bus fences out so a worker cannot recursively
// dispatch. The fence is a bus-stamped marker (ClientEntry.SpawnedBy), not the
// weakly-enforced kind.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wireapi"
)

// dialKind mints a top-level credential (SpawnedBy empty) of a specific kind and
// connects an SDK client with it.
func dialKind(t *testing.T, b *bus.Bus, name, kind string) *sextant.Client {
	t.Helper()
	creds, _, err := b.MintClient(t.Context(), name, kind)
	if err != nil {
		t.Fatalf("MintClient(%s, %s): %v", name, kind, err)
	}
	return connectCreds(t, b, name, creds)
}

// connectCreds writes a credential to a temp file and connects an SDK client.
func connectCreds(t *testing.T, b *bus.Bus, name, creds string) *sextant.Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: path,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestMintOnBehalfTopLevelClientDispatches: a top-level client (any kind) may mint
// children with its own authority — no operator credential, no blessed kind.
func TestMintOnBehalfTopLevelClientDispatches(t *testing.T) {
	b := startBus(t)
	ctx := readCtx(t)
	for _, kind := range []string{wireapi.KindAgent, wireapi.KindClient, "worker"} {
		disp := dialKind(t, b, "disp-"+kind, kind)
		child, err := disp.Register(ctx, "child-"+kind, wireapi.KindAgent)
		if err != nil {
			t.Errorf("a top-level kind=%q client could not dispatch: %v", kind, err)
			continue
		}
		if child.ID == "" || child.Creds == "" {
			t.Errorf("kind=%q: issued child is incomplete: %+v", kind, child)
		}
	}
}

// TestMintOnBehalfSpawnedWorkerCannotDispatch: the one fence — a client the bus
// spawned on another's behalf may not itself mint children.
func TestMintOnBehalfSpawnedWorkerCannotDispatch(t *testing.T) {
	b := startBus(t)
	ctx := readCtx(t)

	disp := dialKind(t, b, "dispatcher", wireapi.KindAgent)
	child, err := disp.Register(ctx, "worker-1", wireapi.KindAgent)
	if err != nil {
		t.Fatalf("dispatcher mint-on-behalf: %v", err)
	}

	// Connect AS the spawned worker; it must be refused when it tries to dispatch.
	worker := connectCreds(t, b, "worker-1", child.Creds)
	if _, err := worker.Register(ctx, "grandchild", wireapi.KindAgent); err == nil {
		t.Errorf("a spawned worker minted a client; a spawned worker must not dispatch")
	}

	// But the dispatcher itself can keep minting (this is what makes recursion work:
	// a worker requests, the non-spawned dispatcher mints).
	if _, err := disp.Register(ctx, "worker-2", wireapi.KindAgent); err != nil {
		t.Errorf("the dispatcher should still be able to mint more workers: %v", err)
	}
}

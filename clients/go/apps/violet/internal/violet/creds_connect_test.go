package violet

// TestVioletConnectsUnderOwnCreds is the slice-E proof for goal.violet's
// real-sdk-secure + one-client-signal-not-manage criteria and TASK-158:
// violet MUST connect under its OWN scoped credential and MUST NOT run under
// the operator's ambient cred.
//
// This test exercises the PRODUCTION connect path — the actual sextant.Connect
// with CredsPath — not a fake. It is deliberately NOT behind the e2e build tag
// so it runs in the standard unit-test gate (go test -race ./internal/violet/...)
// and keeps fakes away from the seam under proof.
//
// The two properties proven:
//
//  1. own-creds: a client connected with violet's creds carries violet's
//     bus-minted ULID as its identity (c.ID()), NOT the operator's.
//
//  2. fail-loud: sextant.Connect returns a non-nil error when CredsPath is
//     empty — the same guard main.go builds on (it fatals if --creds is "").
//     Both the SDK and the entrypoint enforce this, so there is no path where
//     violet connects with zero credentials.
//
// Why this seam, not an e2e test or a unit test against a fake:
//   - It exercises sextant.Connect (the real dial + identity parse), not a
//     hypothetical wrapper.
//   - bus.Start + b.MintClient are the same primitives pkg/sextant's own tests
//     use — the bus is in-process, hermetic, and fast.
//   - NewSDKAdapter wraps the connected client exactly as cmd/sextant-violet's
//     main does; the adapter.ID() / adapter.Principal() calls are the values
//     main passes into violet.New and logf — not a fake that could be generous
//     about what an empty-creds connect would return.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/go/sdk"
)

// TestVioletConnectsUnderOwnCreds proves the TASK-158 invariant via the real
// sextant.Connect path: violet's identity after connect is its own minted ULID
// (never the operator's), and connecting without any creds fails loud.
func TestVioletConnectsUnderOwnCreds(t *testing.T) {
	// Start an in-process, hermetic bus — the same primitive pkg/sextant uses.
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	// Mint the operator's creds separately — violet must NOT use these.
	operatorCreds, operatorID, err := b.MintClient(t.Context(), "operator", "human")
	if err != nil {
		t.Fatalf("MintClient(operator): %v", err)
	}
	operatorCredsFile := writeTempCredsFile(t, operatorCreds)

	// Mint violet's OWN scoped creds — the production recipe is:
	//   sextant clients register violet --kind agent --out violet.creds
	violetCreds, violetID, err := b.MintClient(t.Context(), "violet", "agent")
	if err != nil {
		t.Fatalf("MintClient(violet): %v", err)
	}
	violetCredsFile := writeTempCredsFile(t, violetCreds)

	if violetID == operatorID {
		t.Fatalf("bus issued the same ULID for violet and operator — test setup broken")
	}

	// --- property 1: own-creds -----------------------------------------------
	// Connect via the REAL sextant.Connect — this is the exact call main.go makes
	// (sextant.Connect(ctx, sextant.Options{CredsPath: *creds, URL: ..., ...})).
	// The connected client's ID comes from the credential itself (identityFromCreds),
	// so it cannot be the operator's ID regardless of what the caller passes.
	violetClient, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: violetCredsFile,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("sextant.Connect(violet creds): %v", err)
	}
	t.Cleanup(func() { _ = violetClient.Close() })

	// Wrap with NewSDKAdapter — identical to cmd/sextant-violet/main.go:
	//   v := violet.New(violet.NewSDKAdapter(c), ...)
	adapter := NewSDKAdapter(violetClient)

	// The adapter must carry violet's ULID, not the operator's.
	if gotID := adapter.ID(); gotID != violetID {
		t.Errorf("adapter.ID() = %q, want violet's own id %q", gotID, violetID)
	}
	if gotID := adapter.ID(); gotID == operatorID {
		t.Errorf("adapter.ID() = operator's id %q — violet is running under the operator's creds (TASK-158 violated)", operatorID)
	}

	// Connect the operator client and verify the two identities are distinct on
	// the live bus — not just at minting time.
	operatorClient, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: operatorCredsFile,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("sextant.Connect(operator creds): %v", err)
	}
	t.Cleanup(func() { _ = operatorClient.Close() })

	if operatorClient.ID() == violetClient.ID() {
		t.Errorf("operator and violet share the same connected identity %q — credentials are not scoped (TASK-158 violated)", violetClient.ID())
	}
	t.Logf("own-creds: violet=%s  operator=%s  (distinct, as required)", violetID, operatorID)

	// --- property 2: fail-loud -----------------------------------------------
	// sextant.Connect with empty CredsPath must return a non-nil error. This is
	// the same guard main.go relies on: it fatals if --creds == "", AND
	// sextant.Connect itself rejects an empty CredsPath at the SDK layer too —
	// double enforcement, so there is no path where violet connects with no creds.
	_, connectErr := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: "", // deliberately empty — the forbidden state
		Logf:      func(string, ...any) {},
	})
	if connectErr == nil {
		t.Fatal("sextant.Connect(empty creds) succeeded — fail-loud guard is broken")
	}
	t.Logf("fail-loud: sextant.Connect(empty creds) = %v", connectErr)
}

// writeTempCredsFile writes NATS credential text to a temp file under t.TempDir()
// and returns its path. The file is cleaned up automatically when the test ends.
func writeTempCredsFile(t *testing.T, creds string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatalf("writeTempCredsFile: %v", err)
	}
	return path
}

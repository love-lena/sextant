package bus

import (
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
)

// TestPrincipalSetIsOperatorOnly (AC#5) is the security-critical one: a
// client-tier caller's principal.set is DENIED by the bus. The pattern mirrors
// clients.retire's operator-only gate — the allow-list lets the client PUBLISH the
// call under its own prefix, so the request reaches the bus; the bus then rejects it
// on authorization (a client ULID is neither the operator nor the enrollment
// credential). Proving the gate at the bus, not the absence of a CLI command, is
// the point: the spine of ADR-0030/0031 is that an agent or peer can never claim
// or alter the designation.
func TestPrincipalSetIsOperatorOnly(t *testing.T) {
	b := startTestBus(t)
	nc, id := connectClient(t, b, "agent-noset")

	resp := call(t, nc, id, wireapi.OpPrincipalSet, wireapi.PrincipalSetInput{Principal: id})
	if resp.Error == "" {
		t.Fatal("a client-tier caller must not be authorized to set the principal")
	}
	if !strings.Contains(resp.Error, "only the operator or enrollment credential may claim the principal") {
		t.Errorf("expected the bootstrap-tier claim gate error, got: %s", resp.Error)
	}

	// The designation is unchanged: still the bootstrap default (the operator seat).
	get := call(t, nc, id, wireapi.OpPrincipalGet, wireapi.PrincipalGetInput{})
	if get.Error != "" {
		t.Fatalf("principal.get: %s", get.Error)
	}
	var out wireapi.PrincipalGetOutput
	mustJSON(t, get.Result, &out)
	if out.Principal != wireapi.OperatorID {
		t.Errorf("principal after a denied client set = %q, want unchanged %q", out.Principal, wireapi.OperatorID)
	}
}

// TestPrincipalBootstrapWritesMetaKey pins the bootstrap write to the right key
// and bucket (sx_meta/principal), defaulting to the operator's seat (AC#3),
// observed through the bus's own operator connection (a client has no direct KV
// access).
func TestPrincipalBootstrapWritesMetaKey(t *testing.T) {
	b := startTestBus(t)
	val, _, err := b.backend.Get(testCtx(t), sx.BucketMeta, sx.MetaKeyPrincipal)
	if err != nil {
		t.Fatalf("read sx_meta/principal: %v", err)
	}
	if string(val) != wireapi.OperatorID {
		t.Errorf("bootstrap principal key = %q, want %q", val, wireapi.OperatorID)
	}
}

// TestPrincipalSurvivesRestart: a re-designation outlives a bus restart of the
// same store. Bootstrap defaults the principal only when the key is absent, so a
// restart must not stomp an operator's set value back to the default — the
// two-way door stays where the operator pointed it.
func TestPrincipalSurvivesRestart(t *testing.T) {
	store := t.TempDir()
	b1, err := Start(t.Context(), Config{StoreDir: store})
	if err != nil {
		t.Fatalf("Start b1: %v", err)
	}
	target := "01HZZZRESTARTPRINCIPALXXXX"
	if _, err := b1.backend.Put(testCtx(t), sx.BucketMeta, sx.MetaKeyPrincipal, []byte(target)); err != nil {
		t.Fatalf("set principal: %v", err)
	}
	b1.Shutdown()

	// Give the embedded server a beat to release its store lock before re-starting.
	deadline := time.Now().Add(3 * time.Second)
	var b2 *Bus
	for {
		b2, err = Start(t.Context(), Config{StoreDir: store})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restart against the same store never succeeded: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(b2.Shutdown)
	val, _, err := b2.backend.Get(testCtx(t), sx.BucketMeta, sx.MetaKeyPrincipal)
	if err != nil {
		t.Fatalf("read principal after restart: %v", err)
	}
	if string(val) != target {
		t.Errorf("principal after restart = %q, want preserved %q (not the default %q)", val, target, wireapi.OperatorID)
	}
}

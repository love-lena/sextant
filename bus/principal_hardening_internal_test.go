package bus

import (
	"encoding/json"
	"testing"

	"github.com/love-lena/sextant/protocol/wireapi"
)

// These tests pin the principal designation's asymmetric authorization
// (ADR-0031), exercising the handler directly so the caller identity (operator /
// enroll / a client ULID) is explicit. The shape:
//   - claiming the UNCLAIMED default: bootstrap tier (operator|enroll) only, and
//     only to a kind=client target;
//   - re-pointing an ESTABLISHED principal: operator only, and only with force.

func mintKind(t *testing.T, b *Bus, kind string) string {
	t.Helper()
	_, id, err := b.MintClient(testCtx(t), "seat-"+kind, kind)
	if err != nil {
		t.Fatalf("mint %s: %v", kind, err)
	}
	return id
}

func setPrincipalAs(t *testing.T, b *Bus, caller string, in wireapi.PrincipalSetInput) error {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal principal.set input: %v", err)
	}
	_, err = b.opPrincipalSet(testCtx(t), caller, data)
	return err
}

func principalNow(t *testing.T, b *Bus) string {
	t.Helper()
	p, err := b.readPrincipal(testCtx(t))
	if err != nil {
		t.Fatalf("read principal: %v", err)
	}
	return p
}

// The enrollment credential may claim an unclaimed principal for a client seat —
// this is what makes `register --self` self-designating with no extra command.
func TestPrincipalFirstClaimByEnrollToClientSeat(t *testing.T) {
	b := startTestBus(t)
	clientID := mintKind(t, b, wireapi.KindClient)
	if err := setPrincipalAs(t, b, wireapi.EnrollID, wireapi.PrincipalSetInput{Principal: clientID}); err != nil {
		t.Fatalf("enroll claim of an unclaimed principal to a client seat should succeed: %v", err)
	}
	if got := principalNow(t, b); got != clientID {
		t.Errorf("principal = %q, want claimed %q", got, clientID)
	}
}

// Human-only at the source: an agent seat can never be claimed as the principal,
// even by the bootstrap tier. This is what keeps an auto-minting agent
// (kind=agent) off the principal by construction.
func TestPrincipalFirstClaimRejectsAgentTarget(t *testing.T) {
	b := startTestBus(t)
	agentID := mintKind(t, b, wireapi.KindAgent)
	if err := setPrincipalAs(t, b, wireapi.EnrollID, wireapi.PrincipalSetInput{Principal: agentID}); err == nil {
		t.Fatal("claiming the principal for an agent seat must be rejected (human-only at the source)")
	}
	if got := principalNow(t, b); got != wireapi.OperatorID {
		t.Errorf("principal after rejected agent claim = %q, want unchanged %q", got, wireapi.OperatorID)
	}
}

// A client-tier ULID caller may not claim the principal — only the bootstrap
// tier (operator|enroll) can. (This is the security-critical guarantee from
// ADR-0030: an agent or peer can never claim the designation.)
func TestPrincipalFirstClaimDeniedToClientCaller(t *testing.T) {
	b := startTestBus(t)
	clientID := mintKind(t, b, wireapi.KindClient)
	if err := setPrincipalAs(t, b, clientID, wireapi.PrincipalSetInput{Principal: clientID}); err == nil {
		t.Fatal("a client-tier caller must not be able to claim the principal")
	}
	if got := principalNow(t, b); got != wireapi.OperatorID {
		t.Errorf("principal after denied client claim = %q, want unchanged %q", got, wireapi.OperatorID)
	}
}

// Re-pointing an established principal requires force; without it the change is
// refused and the designation is unchanged. With force it proceeds.
func TestPrincipalRepointRequiresForce(t *testing.T) {
	b := startTestBus(t)
	a := mintKind(t, b, wireapi.KindClient)
	bID := mintKind(t, b, wireapi.KindClient)
	if err := setPrincipalAs(t, b, wireapi.OperatorID, wireapi.PrincipalSetInput{Principal: a}); err != nil {
		t.Fatalf("operator first claim: %v", err)
	}
	if err := setPrincipalAs(t, b, wireapi.OperatorID, wireapi.PrincipalSetInput{Principal: bID}); err == nil {
		t.Fatal("re-pointing an established principal without force must be refused")
	}
	if got := principalNow(t, b); got != a {
		t.Errorf("principal after refused re-point = %q, want unchanged %q", got, a)
	}
	if err := setPrincipalAs(t, b, wireapi.OperatorID, wireapi.PrincipalSetInput{Principal: bID, Force: true}); err != nil {
		t.Fatalf("forced re-point should succeed: %v", err)
	}
	if got := principalNow(t, b); got != bID {
		t.Errorf("principal after forced re-point = %q, want %q", got, bID)
	}
}

// Only the operator may re-point an established principal — enroll may make the
// first claim but may not move an established one, even with force.
func TestPrincipalRepointIsOperatorOnly(t *testing.T) {
	b := startTestBus(t)
	a := mintKind(t, b, wireapi.KindClient)
	bID := mintKind(t, b, wireapi.KindClient)
	if err := setPrincipalAs(t, b, wireapi.EnrollID, wireapi.PrincipalSetInput{Principal: a}); err != nil {
		t.Fatalf("enroll first claim: %v", err)
	}
	if err := setPrincipalAs(t, b, wireapi.EnrollID, wireapi.PrincipalSetInput{Principal: bID, Force: true}); err == nil {
		t.Fatal("enroll must not be able to re-point an established principal")
	}
	if got := principalNow(t, b); got != a {
		t.Errorf("principal after rejected enroll re-point = %q, want unchanged %q", got, a)
	}
}

// Setting the principal to its current value is not a change, so it does not
// require force (idempotent).
func TestPrincipalRepointToSameValueNeedsNoForce(t *testing.T) {
	b := startTestBus(t)
	a := mintKind(t, b, wireapi.KindClient)
	if err := setPrincipalAs(t, b, wireapi.OperatorID, wireapi.PrincipalSetInput{Principal: a}); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := setPrincipalAs(t, b, wireapi.OperatorID, wireapi.PrincipalSetInput{Principal: a}); err != nil {
		t.Fatalf("setting the principal to its current value should be a no-op, not require force: %v", err)
	}
}

package main

import (
	"context"
	"testing"

	"github.com/love-lena/sextant/shared/go/clictx"
)

// TestNamedIdentityResolvesWithoutDisturbing124 is TASK-76's gate: a named crew
// agent (sirius/canopus/vega) pinned via $SEXTANT_CONTEXT must connect as its
// REGISTERED identity from the first connect, AND that must not disturb 124's
// sub-restore. The two assertions are deliberately independent — they exercise
// the two orthogonal halves the design pass calls out:
//
//	(1) IDENTITY: resolve() returns the named context, never the auto-mint path.
//	    A pinned $SEXTANT_CONTEXT lands on resolve()'s TOP branch (1), above 124's
//	    re-pin (branch 2) and the auto-mint (branch 4). The mint stub fails the
//	    test if hit.
//	(2) SUB-RESTORE (124): channelHub.restoreSubs still acts on the persisted
//	    subjects on that connect — it gates every primed subject's live cursor
//	    synchronously before spawning the async restore. restoreSubs is keyed on
//	    the durable substate, NOT on which branch resolved the identity, so it is
//	    orthogonal to TASK-76 and stays fully load-bearing.
func TestNamedIdentityResolvesWithoutDisturbing124(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())

	// A registered named crew identity (an agent context), exactly as a per-agent
	// launch wrapper pins it via $SEXTANT_CONTEXT=canopus.
	if err := clictx.Save(clictx.Context{
		Name: "canopus", URL: "nats://crew", ID: "01CANOPUS", Kind: agentKind, Creds: "/canopus.creds",
	}); err != nil {
		t.Fatal(err)
	}

	// The durable substate this session carries across resume names a subject the
	// agent was following, with a primed cursor (a frame was delivered before the
	// resume). restoreSubs must gate it on connect (124's K-class guard).
	state := loadSubstate(t.TempDir(), "sess-canopus")
	state.addSubject("msg.topic.crew", "")
	state.advance("msg.topic.crew", 5) // primed → restoreSubs must gate it

	// $SEXTANT_CONTEXT=canopus → resolve()'s explicit-context branch (1).
	m := &connManager{cf: cf("", t.TempDir(), "", "canopus"), state: state}
	m.mint = failMint(t) // (1): the auto-mint path must NOT be taken.

	// (1) IDENTITY: resolve returns the registered name, not an auto-mint id.
	rc, err := m.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Context != "canopus" || rc.Creds != "/canopus.creds" {
		t.Fatalf("resolved %+v, want the registered identity canopus (not an auto-mint)", rc)
	}

	// (2) SUB-RESTORE (124): the named-identity resolve leaves 124's restore path
	// fully load-bearing. restoreSubs is keyed on the durable substate — the SAME
	// state the named connect carries — NOT on which branch resolved the identity,
	// so the two are orthogonal. First, the followed subject survives the
	// named-identity resolve in the durable store (124's source of truth, untouched).
	if _, subs := state.snapshot(); subs["msg.topic.crew"].Seq != 5 {
		t.Fatalf("the followed subject did not survive the named-identity resolve: %v", subs)
	}

	// Then exercise restoreSubs' real synchronous path — gatePrimedForRestore, the
	// pre-goroutine half restoreSubs runs on every connect (the part that races a
	// tool message_subscribe). It must snapshot the connect's subjects AND gate the
	// primed one for catch-up (TASK-124's K-class guard). We call the production
	// helper directly so a regression that removed the gating WOULD fail here; the
	// async restore (network I/O against a live client) is covered by restore_test.go.
	h := newChannelHub((&recorder{}).notify, staticNames(nil))
	h.state = state

	subjects := h.gatePrimedForRestore()
	if _, ok := subjects["msg.topic.crew"]; !ok {
		t.Fatal("restoreSubs' snapshot dropped the persisted subject — 124 sub-restore disturbed by the named-identity resolve")
	}
	if !h.isCatchingUp("msg.topic.crew") {
		t.Error("restoreSubs did not gate the persisted primed subject for catch-up — 124's K-class guard not fired on the named-identity connect")
	}
}

// TestNamedIdentitySwitchedRePinStillResolves guards the composability claim from
// the other direction: 124's CONTEXT re-pin (restorePersistedContext → branch 2)
// remains the mid-session-switch fallback for an agent that context_use-switched
// without a launch-pinned name. TASK-76 sits ABOVE this (branch 1); when neither
// is set the re-pin still resolves the named identity. Asserts the re-pin path is
// intact and never reaches the auto-mint.
func TestNamedIdentitySwitchedRePinStillResolves(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{
		Name: "vega", URL: "nats://crew", ID: "01VEGA", Kind: agentKind, Creds: "/vega.creds",
	}); err != nil {
		t.Fatal(err)
	}

	// No launch env pin; the durable state carries a prior context_use switch.
	state := loadSubstate(t.TempDir(), "sess-vega")
	state.setContext("vega")

	m := &connManager{cf: cf("", t.TempDir(), "", ""), state: state}
	m.restorePersistedContext() // 124 mode C: re-pin the switched identity.
	m.mint = failMint(t)

	rc, err := m.resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rc.Context != "vega" || rc.Creds != "/vega.creds" {
		t.Fatalf("resolved %+v, want vega via 124's re-pin (branch 2)", rc)
	}
}

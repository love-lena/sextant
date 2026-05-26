package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestResolveAgentRefUUIDPassthrough proves that a UUID-shaped `ref` is
// returned without a list_agents round trip. The lister is wired to
// fail loudly if it gets called — UUIDs MUST never hit the bus.
func TestResolveAgentRefUUIDPassthrough(t *testing.T) {
	want := uuid.New()
	called := false
	lister := func() ([]sextantproto.AgentSummary, error) {
		called = true
		return nil, errors.New("lister must not be called for UUID input")
	}
	got, err := resolveAgentRefWithLister(want.String(), lister)
	if err != nil {
		t.Fatalf("resolveAgentRefWithLister: %v", err)
	}
	if got != want {
		t.Errorf("uuid = %s, want %s", got, want)
	}
	if called {
		t.Error("lister was called for UUID input")
	}
}

// TestResolveAgentRefNameResolves proves a name-shaped ref resolves to
// the matching non-archived agent's UUID via list_agents.
func TestResolveAgentRefNameResolves(t *testing.T) {
	want := uuid.New()
	lister := func() ([]sextantproto.AgentSummary, error) {
		return []sextantproto.AgentSummary{
			{UUID: uuid.New(), Name: "other", Lifecycle: string(sextantproto.LifecycleRunning)},
			{UUID: want, Name: "agent-foo", Lifecycle: string(sextantproto.LifecycleDefined)},
		}, nil
	}
	got, err := resolveAgentRefWithLister("agent-foo", lister)
	if err != nil {
		t.Fatalf("resolveAgentRefWithLister: %v", err)
	}
	if got != want {
		t.Errorf("uuid = %s, want %s", got, want)
	}
}

// TestResolveAgentRefIgnoresArchived proves the resolver skips agents in
// lifecycle=archived so a fresh agent with a recycled name doesn't
// collide with the old archived holder of that name.
func TestResolveAgentRefIgnoresArchived(t *testing.T) {
	live := uuid.New()
	lister := func() ([]sextantproto.AgentSummary, error) {
		return []sextantproto.AgentSummary{
			{UUID: uuid.New(), Name: "agent-foo", Lifecycle: string(sextantproto.LifecycleArchived)},
			{UUID: live, Name: "agent-foo", Lifecycle: string(sextantproto.LifecycleDefined)},
		}, nil
	}
	got, err := resolveAgentRefWithLister("agent-foo", lister)
	if err != nil {
		t.Fatalf("resolveAgentRefWithLister: %v", err)
	}
	if got != live {
		t.Errorf("uuid = %s, want %s (live agent, not the archived one)", got, live)
	}
}

// TestResolveAgentRefAmbiguous proves the resolver surfaces an error
// when two non-archived agents share a name. The uniqueness invariant
// should prevent this, but the operator deserves a clear message rather
// than an arbitrary pick.
func TestResolveAgentRefAmbiguous(t *testing.T) {
	lister := func() ([]sextantproto.AgentSummary, error) {
		return []sextantproto.AgentSummary{
			{UUID: uuid.New(), Name: "agent-foo", Lifecycle: string(sextantproto.LifecycleRunning)},
			{UUID: uuid.New(), Name: "agent-foo", Lifecycle: string(sextantproto.LifecycleDefined)},
		}, nil
	}
	_, err := resolveAgentRefWithLister("agent-foo", lister)
	if err == nil {
		t.Fatal("expected error on ambiguous name")
	}
	if !strings.Contains(err.Error(), "multiple") {
		t.Errorf("error %q does not mention multiple matches", err)
	}
}

// TestResolveAgentRefNoMatch proves the resolver returns an error when
// no non-archived agent owns the given name. Archived-only matches
// don't count because their names are released.
func TestResolveAgentRefNoMatch(t *testing.T) {
	lister := func() ([]sextantproto.AgentSummary, error) {
		return []sextantproto.AgentSummary{
			{UUID: uuid.New(), Name: "other", Lifecycle: string(sextantproto.LifecycleRunning)},
			{UUID: uuid.New(), Name: "agent-foo", Lifecycle: string(sextantproto.LifecycleArchived)},
		}, nil
	}
	_, err := resolveAgentRefWithLister("agent-foo", lister)
	if err == nil {
		t.Fatal("expected error on no match")
	}
	if !strings.Contains(err.Error(), "agent-foo") {
		t.Errorf("error %q does not mention the missing ref", err)
	}
}

// TestResolveAgentRefListerError proves a transport-level lister error
// is wrapped and surfaced rather than swallowed.
func TestResolveAgentRefListerError(t *testing.T) {
	boom := errors.New("nats: timeout")
	lister := func() ([]sextantproto.AgentSummary, error) {
		return nil, boom
	}
	_, err := resolveAgentRefWithLister("agent-foo", lister)
	if err == nil {
		t.Fatal("expected error when lister fails")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error %v does not wrap %v", err, boom)
	}
}

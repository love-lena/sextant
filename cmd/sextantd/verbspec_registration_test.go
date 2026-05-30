package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/natsboot"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// noopHandler is a registrable Handler that does nothing — registration
// only needs a non-nil Handler, so the order/completeness tests don't
// build real handlers.
func noopHandler() rpc.Handler {
	return func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		emit(sextantproto.RPCResponse{Terminal: true})
		return nil
	}
}

// newTestRPCServer boots a throwaway nats-server and returns an
// rpc.Server bound to it. Skips when nats-server isn't on PATH.
func newTestRPCServer(t *testing.T) *rpc.Server {
	t.Helper()
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}
	dir := t.TempDir()
	cfg := natsboot.DefaultConfig(filepath.Join(dir, "nats"))
	srv, err := natsboot.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("natsboot.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})
	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("srv.Connect: %v", err)
	}
	t.Cleanup(nc.Close)
	rpcSrv, err := rpc.New(nc, rpc.Config{From: sextantproto.Address{
		Kind: sextantproto.AddressDaemon,
		ID:   "test",
	}})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	return rpcSrv
}

// factoriesForPhase builds a complete no-op factory map for every verb
// the table tags with phase p — i.e. exactly what a correct daemon
// registration step supplies, minus the real handler deps.
func factoriesForPhase(p rpc.Phase) map[string]func() rpc.Handler {
	m := map[string]func() rpc.Handler{}
	for _, s := range rpc.VerbSpecsForPhase(p) {
		m[s.Name] = noopHandler
	}
	return m
}

// TestRegisterPhasePreservesTableOrder asserts the staged registration
// the VerbSpec table drives registers each phase's verbs in exact table
// order — the two-phase (now three-phase) registration ORDER the ticket
// requires preserved. registerPhase iterates rpc.VerbSpecsForPhase, so
// the registration order is the table order by construction; this test
// pins it observationally via Server.RegisteredVerbs.
func TestRegisterPhasePreservesTableOrder(t *testing.T) {
	srv := newTestRPCServer(t)

	// Register in the daemon's real staged order: Initial, Lifecycle,
	// Worktree.
	for _, p := range []rpc.Phase{rpc.PhaseInitial, rpc.PhaseLifecycle, rpc.PhaseWorktree} {
		if err := registerPhase(srv, p, factoriesForPhase(p)); err != nil {
			t.Fatalf("registerPhase(%d): %v", p, err)
		}
	}

	// Expected order = table order across the three phases (the table is
	// authored Initial → Lifecycle → Worktree, which is the staged order).
	var want []string
	for _, p := range []rpc.Phase{rpc.PhaseInitial, rpc.PhaseLifecycle, rpc.PhaseWorktree} {
		for _, s := range rpc.VerbSpecsForPhase(p) {
			want = append(want, s.Name)
		}
	}
	got := srv.RegisteredVerbs()
	if len(got) != len(want) {
		t.Fatalf("registered %d verbs, table has %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("registration order[%d] = %q, want %q (full got=%v want=%v)", i, got[i], want[i], got, want)
		}
	}
}

// TestRegisterPhaseEveryVerbHasHandler is the table-completeness
// regression: every VerbSpec across every phase gets a registered
// handler when the daemon runs its staged registration. A verb without a
// handler is exactly the drift class the single table prevents.
func TestRegisterPhaseEveryVerbHasHandler(t *testing.T) {
	srv := newTestRPCServer(t)
	for _, p := range []rpc.Phase{rpc.PhaseInitial, rpc.PhaseLifecycle, rpc.PhaseWorktree} {
		if err := registerPhase(srv, p, factoriesForPhase(p)); err != nil {
			t.Fatalf("registerPhase(%d): %v", p, err)
		}
	}
	registered := make(map[string]bool)
	for _, v := range srv.RegisteredVerbs() {
		registered[v] = true
	}
	for _, s := range rpc.VerbSpecs {
		if !registered[s.Name] {
			t.Errorf("verb %q (phase %d) has a VerbSpec but no registered handler", s.Name, s.Phase)
		}
		// Every registered verb must also resolve a capability via the
		// same table (no handler without a capability mapping).
		if rpc.CapFor(s.Name) != s.Capability {
			t.Errorf("verb %q: CapFor=%q, spec.Capability=%q", s.Name, rpc.CapFor(s.Name), s.Capability)
		}
	}
}

// TestRegisterPhaseRejectsMissingFactory proves registerPhase fails loudly
// when a phase verb has no handler factory — the "no verb without a
// handler" half of the completeness invariant.
func TestRegisterPhaseRejectsMissingFactory(t *testing.T) {
	srv := newTestRPCServer(t)
	factories := factoriesForPhase(rpc.PhaseInitial)
	// Drop one verb's factory.
	delete(factories, rpc.VerbGetVersion)
	if err := registerPhase(srv, rpc.PhaseInitial, factories); err == nil {
		t.Fatal("registerPhase must error when a phase verb has no handler factory")
	}
}

// TestRegisterPhaseRejectsOrphanFactory proves registerPhase fails loudly
// when a factory has no matching phase verb — the "no handler without a
// verb" half of the completeness invariant.
func TestRegisterPhaseRejectsOrphanFactory(t *testing.T) {
	srv := newTestRPCServer(t)
	factories := factoriesForPhase(rpc.PhaseInitial)
	factories["bogus_verb_not_in_table"] = noopHandler
	if err := registerPhase(srv, rpc.PhaseInitial, factories); err == nil {
		t.Fatal("registerPhase must error when a factory has no matching phase verb")
	}
}

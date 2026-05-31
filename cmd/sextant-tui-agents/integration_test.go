// integration_test.go — end-to-end agents TUI Component wired to a real
// NATS via pkg/natsboot. Seeds two AgentDefinitions in the
// `agent_definitions` KV, registers the real list_agents handler against
// an rpc.Server, drives the Component's reducer through fetch + Enter,
// and asserts that `ui_state.<operator>.selected_agent` lands with the
// cursor's UUID.
//
// Skipped when `nats-server` is not on PATH — mirrors pkg/client tests.
package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/natsboot"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
	"github.com/love-lena/sextant/pkg/tui/agents"
)

func TestIntegrationEnterPersistsSelectedAgent(t *testing.T) {
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}

	srv := bootNATSForTest(t, bin)

	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("connect for seeding: %v", err)
	}
	defer nc.Close()
	cfgDefaults := natsboot.DefaultConfig("")
	if err := natsboot.Bootstrap(context.Background(), nc, cfgDefaults.MaxBytesPerStream); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	idA := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	idB := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	seedAgent(t, nc, idA, "alpha", sextantproto.LifecycleDefined)
	seedAgent(t, nc, idB, "beta", sextantproto.LifecycleRunning)

	rpcSrv, err := rpc.New(nc, rpc.Config{
		From: sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "test"},
	})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancelRun()
		_ = rpcSrv.Close()
	})
	go func() { _ = rpcSrv.Run(runCtx) }()
	time.Sleep(50 * time.Millisecond) // let subscribe settle

	if err := rpcSrv.Register(rpc.VerbListAgents, handlers.NewListAgents(openAgentDefsKV(t, nc))); err != nil {
		t.Fatalf("register list_agents: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli, err := client.ConnectWithConfig(ctx, client.Config{
		NATS:     client.NATSConfig{URL: srv.PublicURL()},
		Operator: client.OperatorConfig{User: srv.OperatorUser(), Password: srv.OperatorPassword()},
	})
	if err != nil {
		t.Fatalf("client.ConnectWithConfig: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	m := agents.New(agents.Options{Bus: cli, Operator: "tester"})

	// Drive the Init batch through one pass so list_agents fires.
	initCmd := m.Init()
	if initCmd == nil {
		t.Fatal("Init returned no cmd")
	}
	// Init returns a tea.Batch. Resolve it and drain each result into
	// the reducer so the model lands in the loaded state.
	for _, msg := range drainBatch(initCmd) {
		out, _ := m.Update(msg)
		m = out.(*agents.Model)
	}
	if m.AgentsCount() != 2 {
		t.Fatalf("agents = %d, want 2", m.AgentsCount())
	}

	// Move cursor to second agent, then press Enter.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(*agents.Model)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter must issue a PutKV cmd")
	}
	_ = cmd() // executes PutKV; we verify via GetKV below.

	got, err := cli.GetKV(ctx, agents.UIStateBucket, "tester.selected_agent")
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	// The model sorts by name (alpha, beta) so cursor=1 → beta (idB).
	wantUUID := idB.String()
	if string(got) != wantUUID {
		t.Fatalf("KV value = %q, want %q", string(got), wantUUID)
	}
}

// drainBatch resolves a tea.Cmd (typically a tea.Batch) into a flat
// slice of tea.Msg values. Non-batch cmds round-trip as a one-element
// slice. tea.BatchMsg is a slice of cmds; we recursively flatten.
func drainBatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, drainBatch(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// bootNATSForTest spins up a NATS server using a long-lived context so
// the subprocess survives until the test cleans up.
func bootNATSForTest(t *testing.T, bin string) *natsboot.Server {
	t.Helper()
	dir := t.TempDir()
	cfg := natsboot.DefaultConfig(filepath.Join(dir, "nats"))
	cfg.NATSBinary = bin
	srv, err := natsboot.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("natsboot.Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = srv.Stop(stopCtx)
	})
	return srv
}

// seedAgent writes one AgentDefinition into the agent_definitions KV bucket
// so list_agents can return it. The desired LifecycleState is projected
// back onto the spec/status split (RFC §5.2) so AgentDefinition.Lifecycle()
// renders the requested rollup.
func seedAgent(t *testing.T, nc *nats.Conn, id uuid.UUID, name string, lifecycle sextantproto.LifecycleState) {
	t.Helper()
	kv := openAgentDefsKV(t, nc)
	spec, status := specStatusForLifecycle(lifecycle)
	def := sextantproto.AgentDefinition{
		UUID:      id,
		Name:      name,
		Type:      "test",
		Template:  "claude-coder",
		Spec:      spec,
		Status:    status,
		Version:   1,
		CreatedAt: sextantproto.NowTimestamp(),
		UpdatedAt: sextantproto.NowTimestamp(),
	}
	raw, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal AgentDefinition: %v", err)
	}
	if _, err := kv.Put(context.Background(), id.String(), raw); err != nil {
		t.Fatalf("KV Put %s: %v", id, err)
	}
}

// specStatusForLifecycle projects a legacy LifecycleState back onto the
// spec/status split so a seeded record renders the requested rollup via
// AgentDefinition.Lifecycle().
func specStatusForLifecycle(l sextantproto.LifecycleState) (sextantproto.AgentSpec, sextantproto.AgentStatusRecord) {
	switch l {
	case sextantproto.LifecyclePaused:
		return sextantproto.AgentSpec{Desired: sextantproto.DesiredPaused, Generation: 1},
			sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedEnded}
	case sextantproto.LifecycleArchived:
		return sextantproto.AgentSpec{Desired: sextantproto.DesiredArchived, Generation: 1},
			sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedEnded}
	case sextantproto.LifecycleRunning:
		return sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
			sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedRunning, ObservedGeneration: 1}
	case sextantproto.LifecycleCrashedState:
		return sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
			sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedCrashed, ObservedGeneration: 1}
	case sextantproto.LifecycleLostState:
		return sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
			sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedLost, ObservedGeneration: 1}
	case sextantproto.LifecycleEndedState:
		return sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
			sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedEnded, ObservedGeneration: 1}
	default: // LifecycleDefined — desired=run, pre-actuation.
		return sextantproto.AgentSpec{Desired: sextantproto.DesiredRun, Generation: 1},
			sextantproto.AgentStatusRecord{Observed: sextantproto.ObservedPending}
	}
}

func openAgentDefsKV(t *testing.T, nc *nats.Conn) jetstream.KeyValue {
	t.Helper()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	kv, err := js.KeyValue(context.Background(), handlers.AgentDefinitionsBucket)
	if err != nil {
		t.Fatalf("KeyValue %s: %v", handlers.AgentDefinitionsBucket, err)
	}
	return kv
}

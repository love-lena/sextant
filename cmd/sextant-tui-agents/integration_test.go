// integration_test.go — end-to-end TUI model wired to a real NATS via
// pkg/natsboot. Seeds two AgentDefinitions in the `agent_definitions` KV,
// registers the real list_agents handler against an rpc.Server, drives
// the model's Init + Enter through the reducer, and asserts that
// `ui_state.<operator>.selected_agent` lands with the cursor's UUID.
//
// Skipped when `nats-server` is not on PATH — mirrors pkg/client tests.
//
// Plan: plans/bootstrap.md#M13
package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/natsboot"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

func TestIntegrationEnterPersistsSelectedAgent(t *testing.T) {
	bin, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}

	srv := bootNATSForTest(t, bin)

	// Seed two agent definitions in the KV.
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

	// Register the real list_agents handler against an rpc.Server.
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

	// Connect a client and drive the model.
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

	m := newModel(cli, "tester")

	// Run the initial fetch RPC inline.
	loaded := m.fetchAgentsCmd()()
	out, _ := m.Update(loaded)
	mm := out.(*model)
	if loadedMsg, ok := loaded.(agentsLoadedMsg); ok && loadedMsg.err != nil {
		t.Fatalf("fetchAgents err: %v", loadedMsg.err)
	}
	if len(mm.agents) != 2 {
		t.Fatalf("agents = %d, want 2 (got %+v)", len(mm.agents), mm.agents)
	}

	// Move cursor to second agent, then press Enter.
	out, _ = mm.Update(specialKey("down"))
	mm = out.(*model)
	if mm.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", mm.cursor)
	}
	_, cmd := mm.Update(specialKey("enter"))
	if cmd == nil {
		t.Fatal("Enter must issue a PutKV cmd")
	}
	res := cmd()
	if done, ok := res.(kvPutDoneMsg); !ok {
		t.Fatalf("cmd returned %T, want kvPutDoneMsg", res)
	} else if done.err != nil {
		t.Fatalf("PutKV: %v", done.err)
	}

	// Verify via GetKV that the value landed.
	got, err := cli.GetKV(ctx, uiStateBucket, "tester.selected_agent")
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	wantUUID := mm.agents[1].UUID.String()
	if string(got) != wantUUID {
		t.Fatalf("KV value = %q, want %q", string(got), wantUUID)
	}
}

// bootNATSForTest spins up a NATS server using a long-lived context so
// the subprocess survives until the test cleans up. The subprocess is
// torn down on t.Cleanup so no nats-server is left running between
// tests — preserves the M0 orphan-invariant.
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
// so list_agents can return it. Mirrors what M11's spawn handler does
// post-validation.
func seedAgent(t *testing.T, nc *nats.Conn, id uuid.UUID, name string, lifecycle sextantproto.LifecycleState) {
	t.Helper()
	kv := openAgentDefsKV(t, nc)
	def := sextantproto.AgentDefinition{
		UUID:      id,
		Name:      name,
		Type:      "test",
		Template:  "claude-coder",
		Lifecycle: lifecycle,
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

// openAgentDefsKV returns the live agent_definitions bucket. jetstream's
// KeyValue type satisfies handlers.AgentKV directly.
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

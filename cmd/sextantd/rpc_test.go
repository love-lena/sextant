package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// rpcClient builds a pkg/client.Client against the running daemon's
// NATS listener. It reads the operator creds the daemon was started
// with so the password is the same one the daemon's NATS authorizes.
//
// Before returning, it polls list_agents until the RPC server is
// dispatching: the daemon writes its control-socket greeting at step
// 4 of Start() but doesn't register RPC verbs until steps 8-11, so a
// test that races straight into spawn_agent can hit an unknown_verb
// reply against the early dispatcher window. We poll list_agents
// (always-registered) to confirm the RPC surface is alive.
func rpcClient(t *testing.T, h *daemonHarness) *client.Client {
	t.Helper()
	cli := rpcClientWithoutWait(t, h)
	if err := waitForRPCReady(cli, 30*time.Second); err != nil {
		t.Fatalf("waitForRPCReady: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	return cli
}

// rpcClientWithoutWait is rpcClient without the readiness poll.
// Tests that *want* to see the unknown-verb error use this.
func rpcClientWithoutWait(t *testing.T, h *daemonHarness) *client.Client {
	t.Helper()
	rt, err := sextantd.ReadRuntimeInfo(h.cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	creds, err := sextantd.ReadOperatorCreds(h.cfg.NATS.OperatorCreds)
	if err != nil {
		t.Fatalf("ReadOperatorCreds: %v", err)
	}
	cfg := client.Config{
		NATS:     client.NATSConfig{URL: "nats://" + rt.NATSAddr},
		Operator: client.OperatorConfig{User: creds.User, Password: creds.Password},
	}
	// Connect with a long-lived context — when this context cancels
	// the client closes its NATS conn (pkg/client.watchCtx contract).
	// Tying it to t.Cleanup gives us a deterministic teardown.
	connCtx, connCancel := context.WithCancel(context.Background())
	cli, err := client.ConnectWithConfig(connCtx, cfg)
	if err != nil {
		connCancel()
		t.Fatalf("client.ConnectWithConfig: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	t.Cleanup(func() {
		_ = cli.Close()
		connCancel()
	})
	return cli
}

// waitForRPCReady polls list_agents + spawn_agent registration until
// the verbs that the spawn flow needs are wired. We send list_agents
// (the simplest of the always-registered verbs); the catch is that
// list_agents is registered in registerInitialVerbs, which runs in
// startRPC — that runs strictly before registerLifecycleVerbs, so a
// list_agents success doesn't yet imply spawn_agent is wired. We then
// send a spawn_agent against a deliberately bad payload (empty name)
// and assert the reply is bad_request, not unknown_verb.
func waitForRPCReady(cli *client.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := cli.RPC(probeCtx, rpc.VerbSpawnAgent,
			sextantproto.SpawnAgentRequest{}, // intentionally empty: handler emits bad_request
			nil,
		)
		cancel()
		// We expect a structured error from the handler (bad_request
		// because Name is empty). Any structured RPCError counts as
		// "RPC up". An unknown_verb error means the handler isn't
		// registered yet; keep polling.
		var rerr *client.RPCError
		if err == nil {
			return nil
		}
		if errors.As(err, &rerr) {
			if rerr.Code == sextantproto.ErrCodeUnknownVerb {
				lastErr = err
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("timed out without seeing rpc")
	}
	return lastErr
}

// TestDaemonListAgentsReturnsEmpty is the M7 acceptance test for
// list_agents — the registry is empty (no agents have been spawned
// because M11 hasn't shipped) and the verb returns the empty-slice
// response shape rather than an error.
func TestDaemonListAgentsReturnsEmpty(t *testing.T) {
	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var resp sextantproto.ListAgentsResponse
	if err := cli.RPC(ctx, rpc.VerbListAgents, sextantproto.ListAgentsRequest{}, &resp); err != nil {
		t.Fatalf("list_agents RPC: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if resp.Agents == nil {
		t.Fatal("Agents must be a non-nil slice (empty is fine)")
	}
	if len(resp.Agents) != 0 {
		t.Fatalf("Agents = %v, want empty", resp.Agents)
	}
}

// TestDaemonGetAgentStatusUnknown404 is the M7 acceptance test for
// get_agent_status — asking for a random uuid yields a structured
// agent_not_found RPCError.
func TestDaemonGetAgentStatusUnknown404(t *testing.T) {
	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := cli.RPC(ctx, rpc.VerbGetAgentStatus,
		sextantproto.GetAgentStatusRequest{AgentID: uuid.New()},
		nil,
	)
	if err == nil {
		t.Fatal("get_agent_status on unknown UUID must error")
	}
	var rerr *client.RPCError
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *client.RPCError", err)
	}
	if rerr.Code != "agent_not_found" {
		t.Fatalf("Code = %q, want agent_not_found", rerr.Code)
	}
}

// TestDaemonQueryHistoryEmptyTable proves the query_history RPC reaches
// ClickHouse and returns an empty result against the empty `events`
// table. Catches the wiring all the way through (client → NATS → server
// → ClickHouse driver → response decode).
func TestDaemonQueryHistoryEmptyTable(t *testing.T) {
	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	events, err := cli.Query(ctx, client.QueryFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Query: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if events == nil {
		t.Fatal("Query returned nil; M7 contract is empty slice on no match")
	}
	if len(events) != 0 {
		t.Fatalf("len(events) = %d, want 0 against fresh ClickHouse", len(events))
	}
}

// TestDaemonReadFileUnknownAgent pins the M12 contract for read_file:
// the verb is wired through the container backend, so calling it
// against an unknown agent surfaces a structured agent_not_found
// error rather than the M7-era not_implemented stub.
func TestDaemonReadFileUnknownAgent(t *testing.T) {
	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := cli.RPC(ctx, rpc.VerbReadFile,
		sextantproto.ReadFileRequest{AgentID: uuid.New(), Path: "/etc/hosts"},
		nil,
	)
	if err == nil {
		t.Fatal("read_file against unknown agent must error")
	}
	var rerr *client.RPCError
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *client.RPCError", err)
	}
	if rerr.Code != sextantproto.ErrCodeAgentNotFound {
		t.Fatalf("Code = %q, want %q", rerr.Code, sextantproto.ErrCodeAgentNotFound)
	}
}

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// rpcClient builds a pkg/client.Client against the running daemon's
// NATS listener. It reads the operator creds the daemon was started
// with so the password is the same one the daemon's NATS authorizes.
func rpcClient(t *testing.T, h *daemonHarness) *client.Client {
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

// TestDaemonReadFileReturnsNotImplemented pins the M7 stub contract —
// the verb is registered (so unknown-verb fires nothing) but the body
// is intentionally not implemented until M11+.
func TestDaemonReadFileReturnsNotImplemented(t *testing.T) {
	h := startDaemonHarness(t)
	cli := rpcClient(t, h)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := cli.RPC(ctx, rpc.VerbReadFile,
		sextantproto.ReadFileRequest{AgentID: uuid.New(), Path: "/etc/hosts"},
		nil,
	)
	if err == nil {
		t.Fatal("read_file (M7 stub) must error")
	}
	var rerr *client.RPCError
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *client.RPCError", err)
	}
	if rerr.Code != sextantproto.ErrCodeNotImplemented {
		t.Fatalf("Code = %q, want %q", rerr.Code, sextantproto.ErrCodeNotImplemented)
	}
}

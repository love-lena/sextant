package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/mcpserver"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// mcpRuntime owns the live state for the sextantd-side MCP server: the
// operator NATS connection it publishes through, its own driver.Conn for
// query_audit (independent of the RPC server's), and the cancel/done
// pair used to drain the server cleanly on daemon shutdown.
//
// The daemon holds at most one mcpRuntime at a time. Created in
// startMCP; torn down in mcpRuntime.stop.
type mcpRuntime struct {
	server *mcpserver.Server
	nc     *nats.Conn
	chConn driver.Conn

	cancel context.CancelFunc
	done   chan struct{}
}

// startMCP wires the MCP server: opens an operator NATS connection,
// resolves the agent_definitions KV bucket and a ClickHouse driver
// connection for introspection tools, instantiates the server and
// kicks off its Run loop in a background goroutine.
//
// Failure semantics mirror startRPC: any error unwinds partial state
// before returning. The daemon treats an MCP startup error as fatal —
// agents need the tool surface to function.
//
//nolint:contextcheck // mcp run lifetime is detached from Start's ctx; see runCtx below.
func (d *daemon) startMCP(ctx context.Context) (*mcpRuntime, error) {
	natsSrv := d.currentNATS()
	if natsSrv == nil {
		return nil, fmt.Errorf("mcp: no live NATS server")
	}
	chSrv := d.currentClickHouse()
	if chSrv == nil {
		return nil, fmt.Errorf("mcp: no live ClickHouse server")
	}

	// Reconnect-capable: same rationale as startRPC — a NATS crash
	// during startup or steady-state must not orphan this connection.
	// See plans/issues/bug-flake-daemon-restarts-nats-after-kill.md.
	nc, err := natsSrv.Connect(
		nats.MaxReconnects(-1),
		nats.ReconnectWait(500*time.Millisecond),
		nats.ReconnectJitter(100*time.Millisecond, 500*time.Millisecond),
	)
	if err != nil {
		return nil, fmt.Errorf("mcp: operator nats connect: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("mcp: jetstream: %w", err)
	}
	kv, err := js.KeyValue(ctx, handlers.AgentDefinitionsBucket)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("mcp: open kv %s: %w", handlers.AgentDefinitionsBucket, err)
	}
	chConn, err := chSrv.Open(ctx)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("mcp: open clickhouse: %w", err)
	}

	srv, err := mcpserver.New(mcpserver.Config{
		NATS: nc,
		CA:   d.ca,
		From: sextantproto.Address{
			Kind: sextantproto.AddressDaemon,
			ID:   fmt.Sprintf("daemon-%d", d.startedAt.UnixNano()),
		},
		HTTPHost:    d.cfg.MCP.HTTPHost,
		HTTPPort:    d.cfg.MCP.HTTPPort,
		StdioSocket: d.cfg.MCP.StdioSocket,
		AgentKV:     kv,
		QueryDB:     chConn,
	})
	if err != nil {
		_ = chConn.Close()
		nc.Close()
		return nil, fmt.Errorf("mcp: build server: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.Background()) //nolint:contextcheck // detached lifetime
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Run(runCtx); err != nil {
			log.Printf("sextantd: mcp.Server.Run: %v", err)
		}
	}()

	// Wait for the listeners to bind so the caller can immediately read
	// HTTPAddr / StdioSocketPath (used for runtime.json + the
	// "sextantd ready" log line).
	select {
	case <-srv.Ready():
	case <-time.After(10 * time.Second):
		cancel()
		<-done
		_ = chConn.Close()
		nc.Close()
		return nil, fmt.Errorf("mcp: server did not become ready within 10s")
	}

	return &mcpRuntime{
		server: srv,
		nc:     nc,
		chConn: chConn,
		cancel: cancel,
		done:   done,
	}, nil
}

// stop tears the MCP runtime down: cancel its run context, Close the
// server (drains in-flight tool calls and removes the socket), close
// the ClickHouse conn, close the operator NATS conn. Idempotent.
//
// ctx is the parent for the bounded shutdown deadline applied to the
// embedded http.Server's Shutdown call. A canceled ctx works — Server
// applies an internal timeout regardless.
func (r *mcpRuntime) stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	var firstErr error
	if r.server != nil {
		if err := r.server.CloseCtx(ctx); err != nil {
			firstErr = err
		}
	}
	if r.done != nil {
		<-r.done
	}
	if r.chConn != nil {
		if err := r.chConn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.nc != nil {
		r.nc.Close()
	}
	return firstErr
}

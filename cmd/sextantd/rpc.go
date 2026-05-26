package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/rpc"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// rpcRuntime owns the live state for the sextantd-side RPC server:
// the operator NATS connection, the ClickHouse driver.Conn, the
// rpc.Server itself, and the cancel function the daemon uses to wind
// it down at shutdown.
//
// The daemon holds at most one rpcRuntime at a time — the daemon's
// rpc field. It is created in startRPC and torn down in stopRPC.
type rpcRuntime struct {
	server      *rpc.Server
	nc          *nats.Conn
	chConn      driver.Conn
	agentDefsKV jetstream.KeyValue

	cancel context.CancelFunc
	done   chan struct{}
}

// startRPC connects to the operator NATS, opens a ClickHouse driver
// connection for query_history, registers the four initial verbs, and
// kicks off the dispatcher in a background goroutine. Returns the
// runtime handle; the daemon stops it from doShutdown.
//
// Failure semantics: any error here unwinds partial state (NATS
// connection, CH conn) before returning. The daemon treats a startRPC
// error as fatal — the RPC surface is one of the daemon's load-bearing
// services and degraded mode is out of scope for M7.
//
//nolint:contextcheck // see runCtx comment below — dispatcher lifetime is intentionally detached from Start's ctx.
func (d *daemon) startRPC(ctx context.Context) (*rpcRuntime, error) {
	natsSrv := d.currentNATS()
	if natsSrv == nil {
		return nil, fmt.Errorf("rpc: no live NATS server")
	}
	chSrv := d.currentClickHouse()
	if chSrv == nil {
		return nil, fmt.Errorf("rpc: no live ClickHouse server")
	}

	nc, err := natsSrv.Connect()
	if err != nil {
		return nil, fmt.Errorf("rpc: operator nats connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("rpc: jetstream context: %w", err)
	}
	kv, err := js.KeyValue(ctx, handlers.AgentDefinitionsBucket)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("rpc: open kv %s: %w", handlers.AgentDefinitionsBucket, err)
	}

	chConn, err := chSrv.Open(ctx)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("rpc: open clickhouse: %w", err)
	}

	srv, err := rpc.New(nc, rpc.Config{
		From: sextantproto.Address{
			Kind: sextantproto.AddressDaemon,
			ID:   fmt.Sprintf("daemon-%d", d.startedAt.UnixNano()),
		},
		// Bump the per-handler cap so the M11/M12 verbs that drive
		// Docker (spawn_agent, kill_agent, restart_agent, exec_in_container)
		// have headroom. The spec's 10s default is the *client-side*
		// timeout; the server-side SLA can be looser without surprising
		// callers since the client unsubscribes at its own deadline.
		// Picking 120s: covers a cold sidecar-image pull on a slow CI
		// host without going wildly over budget.
		HandlerTimeout: 120 * time.Second,
	})
	if err != nil {
		_ = chConn.Close()
		nc.Close()
		return nil, fmt.Errorf("rpc: build server: %w", err)
	}

	if err := registerInitialVerbs(srv, kv, chConn); err != nil {
		_ = chConn.Close()
		nc.Close()
		return nil, fmt.Errorf("rpc: register handlers: %w", err)
	}

	// Detached lifetime: the dispatcher must outlive Start's ctx,
	// which is canceled as soon as Start returns successfully. We tie
	// runCtx to stopRPC instead so daemon shutdown is the only thing
	// that ends the dispatcher.
	runCtx, cancel := context.WithCancel(context.Background()) //nolint:contextcheck // see comment above
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Run(runCtx); err != nil {
			log.Printf("sextantd: rpc.Server.Run: %v", err)
		}
	}()

	return &rpcRuntime{
		server:      srv,
		nc:          nc,
		chConn:      chConn,
		agentDefsKV: kv,
		cancel:      cancel,
		done:        done,
	}, nil
}

// registerInitialVerbs installs the verbs that have no container-runtime
// dependency. The M7-era set: list_agents, get_agent_status,
// query_history. M12 added query_audit and query_trace (both ClickHouse-
// only, no container deps). The container-touching verbs (read_file,
// list_dir, stat, exec_in_container, restart_agent) wait for the spawn
// runtime in registerLifecycleVerbs.
func registerInitialVerbs(srv *rpc.Server, kv handlers.AgentKV, chConn handlers.QueryHistoryDB) error {
	if err := srv.Register(rpc.VerbListAgents, handlers.NewListAgents(kv)); err != nil {
		return err
	}
	if err := srv.Register(rpc.VerbGetAgentStatus, handlers.NewGetAgentStatus(kv)); err != nil {
		return err
	}
	if err := srv.Register(rpc.VerbQueryHistory, handlers.NewQueryHistory(chConn)); err != nil {
		return err
	}
	if err := srv.Register(rpc.VerbQueryAudit, handlers.NewQueryAudit(chConn)); err != nil {
		return err
	}
	if err := srv.Register(rpc.VerbQueryTrace, handlers.NewQueryTrace(chConn)); err != nil {
		return err
	}
	return nil
}

// registerLifecycleVerbs wires the M11+M12 agent-lifecycle and
// container-filesystem verbs onto the RPC server now that the spawn
// runtime exists. The CA is the daemon's signing CA (rpc handler
// embeds it in every issued JWT).
func (r *rpcRuntime) registerLifecycleVerbs(ca *authjwt.CA, spawnRT *spawnRuntime) error {
	spawnDeps := spawnRT.asSpawnDeps(r.chConn)
	spawnDeps.CA = ca
	if err := r.server.Register(rpc.VerbSpawnAgent, handlers.NewSpawnAgent(spawnDeps)); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbKillAgent, handlers.NewKillAgent(handlers.KillDeps{
		Definitions:  spawnDeps.Definitions,
		Incarnations: spawnDeps.Incarnations,
		Containers:   spawnDeps.Containers,
	})); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbArchiveAgent, handlers.NewArchiveAgent(handlers.ArchiveDeps{
		Definitions:  spawnDeps.Definitions,
		Incarnations: spawnDeps.Incarnations,
		Containers:   spawnDeps.Containers,
		Volumes:      spawnDeps.Volumes,
	})); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbPromptAgent, handlers.NewPromptAgent(handlers.PromptDeps{
		Definitions: spawnDeps.Definitions,
		NATS:        r.nc,
		From: sextantproto.Address{
			Kind: sextantproto.AddressDaemon,
			ID:   "daemon",
		},
	})); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbRestartAgent, handlers.NewRestartAgent(handlers.RestartDeps{
		Definitions:   spawnDeps.Definitions,
		Incarnations:  spawnDeps.Incarnations,
		Containers:    spawnRT.containers,
		Volumes:       spawnRT.containers,
		Templates:     spawnDeps.Templates,
		CA:            ca,
		WorkspaceRoot: spawnDeps.WorkspaceRoot,
		HostID:        spawnDeps.HostID,
		NATSURL:       spawnDeps.NATSURL,
		NATSUser:      spawnDeps.NATSUser,
		NATSPassword:  spawnDeps.NATSPassword,
		MCPURL:        spawnDeps.MCPURL,
		Issuer:        spawnDeps.Issuer,
		TestRunLabel:  spawnDeps.TestRunLabel,
	})); err != nil {
		return err
	}
	filesDeps := handlers.FilesDeps{
		Definitions:  spawnDeps.Definitions,
		Incarnations: spawnDeps.Incarnations,
		Containers:   spawnRT.containers,
	}
	if err := r.server.Register(rpc.VerbReadFile, handlers.NewReadFile(filesDeps)); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbListDir, handlers.NewListDir(filesDeps)); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbStat, handlers.NewStat(filesDeps)); err != nil {
		return err
	}
	if err := r.server.Register(rpc.VerbExecInContainer, handlers.NewExecInContainer(filesDeps)); err != nil {
		return err
	}
	return nil
}

// stopRPC tears the RPC runtime down in reverse order: cancel the
// dispatcher context, Close the server (drains in-flight handlers),
// close the operator NATS conn, close the ClickHouse driver. Idempotent.
func (r *rpcRuntime) stopRPC() error {
	if r == nil {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
	}
	var firstErr error
	if r.server != nil {
		if err := r.server.Close(); err != nil {
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

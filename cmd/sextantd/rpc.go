package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"
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

	// heartbeats is the L1 heartbeat cache wired into prompt_agent.
	// Nil when the cache failed to start (daemon continues; guard skipped).
	heartbeats            handlers.HeartbeatLookup
	heartbeatStaleness    time.Duration
	heartbeatStartupGrace time.Duration

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

	// Reconnect-capable so the RPC server (and the spawn-runtime + control
	// surfaces that share this conn) survives a NATS restart. Without
	// reconnect, a NATS crash during startup (or any time) leaves every
	// downstream Put/Subscribe on this conn permanently broken — see
	// plans/issues/bug-flake-daemon-restarts-nats-after-kill.md for the
	// failure shape this guards against. Knobs match pkg/client and the
	// shared-concerns spec.
	nc, err := natsSrv.Connect(
		nats.MaxReconnects(-1),
		nats.ReconnectWait(500*time.Millisecond),
		nats.ReconnectJitter(100*time.Millisecond, 500*time.Millisecond),
	)
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

	rt := &rpcRuntime{
		server:      srv,
		nc:          nc,
		chConn:      chConn,
		agentDefsKV: kv,
	}

	// agentsDataRoot mirrors what buildSpawnRuntime computes for the
	// spawn handler — surfaced here so get_agent_status can publish the
	// per-agent session-log locators (in-container JSONL path + durable
	// host snapshot path) back to the operator. The persistent
	// claude-projects bind-mount is gone (S0, RFC §5.10); this root now
	// holds the snapshot-on-stop the reconciler writes.
	agentsDataRoot := filepath.Join(d.cfg.Paths.DataDir, "agents")

	if err := registerInitialVerbs(srv, kv, chConn, rt.heartbeatLookup(), d.startedAt, agentsDataRoot); err != nil {
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
	rt.cancel = cancel
	rt.done = done

	return rt, nil
}

// heartbeatLookup returns a HeartbeatLookup that resolves through
// r.heartbeats at call time. The cache is installed *after* startRPC
// (see daemon.go step 11b), so get_agent_status — registered up front
// in registerInitialVerbs — needs this late-binding adapter to find
// the cache once it lands. Returns a non-nil adapter; LastSeen falls
// through to (zero, false) until r.heartbeats is set.
func (r *rpcRuntime) heartbeatLookup() handlers.HeartbeatLookup {
	return rpcHeartbeatLookup{rt: r}
}

// rpcHeartbeatLookup is the late-binding adapter described on
// rpcRuntime.heartbeatLookup. It reads r.heartbeats each call so the
// daemon's startup ordering (register handlers, then install cache)
// doesn't bake a nil into the handler closure.
type rpcHeartbeatLookup struct {
	rt *rpcRuntime
}

func (l rpcHeartbeatLookup) LastSeen(id uuid.UUID) (time.Time, bool) {
	if l.rt == nil || l.rt.heartbeats == nil {
		return time.Time{}, false
	}
	return l.rt.heartbeats.LastSeen(id)
}

// registerInitialVerbs installs the PhaseInitial verbs — those with no
// container-runtime dependency (ClickHouse-only queries, KV reads, the
// get_version diagnostic). The set is whatever rpc.VerbSpecs tags
// PhaseInitial; this function only supplies the per-verb handler
// factories and delegates the iteration + ordering to registerPhase.
// The container-touching verbs wait for the spawn runtime in
// registerLifecycleVerbs (PhaseLifecycle).
//
// heartbeats is the late-binding HeartbeatLookup wrapper (see
// rpcRuntime.heartbeatLookup): the real cache is installed in
// daemon.go step 11b, after this function runs. Nil is acceptable —
// get_agent_status simply skips the heartbeat annotation when callers
// don't ask for it (the only at-startup callers are tests).
//
// startedAt is the daemon process start time, captured at the top of
// daemon.Start. The get_version handler closes over it so each call
// reports the actual boot time rather than the time-of-call.
//
// agentsDataRoot is the per-agent runtime root (`<DataDir>/agents`);
// get_agent_status uses it to compute the session-log locators (the
// in-container JSONL path + the durable host snapshot path) the
// operator's `agents context` verb reads from.
func registerInitialVerbs(srv *rpc.Server, kv handlers.AgentKV, chConn handlers.QueryHistoryDB, heartbeats handlers.HeartbeatLookup, startedAt time.Time, agentsDataRoot string) error {
	factories := map[string]func() rpc.Handler{
		rpc.VerbListAgents: func() rpc.Handler { return handlers.NewListAgents(kv) },
		rpc.VerbGetAgentStatus: func() rpc.Handler {
			return handlers.NewGetAgentStatusWithDeps(handlers.GetAgentStatusDeps{
				KV:             kv,
				Heartbeats:     heartbeats,
				AgentsDataRoot: agentsDataRoot,
			})
		},
		rpc.VerbQueryHistory: func() rpc.Handler { return handlers.NewQueryHistory(chConn) },
		rpc.VerbQueryAudit:   func() rpc.Handler { return handlers.NewQueryAudit(chConn) },
		rpc.VerbQueryTrace:   func() rpc.Handler { return handlers.NewQueryTrace(chConn) },
		rpc.VerbGetVersion: func() rpc.Handler {
			return handlers.NewGetVersion(handlers.VersionDeps{StartedAt: startedAt})
		},
	}
	return registerPhase(srv, rpc.PhaseInitial, factories)
}

// registerPhase wires every VerbSpec in phase p onto srv by looking up
// its handler factory in factories and registering in table order. It
// enforces the table-completeness invariant the VerbSpec table exists to
// guarantee (RFC §5.8): a phase verb without a factory, or a factory
// without a phase verb, is a wiring bug and fails startup loudly rather
// than silently leaving a verb undispatched. Capability and schema are
// already derived from the same table, so one row is the only thing to
// add for a new verb.
func registerPhase(srv *rpc.Server, p rpc.Phase, factories map[string]func() rpc.Handler) error {
	specs := rpc.VerbSpecsForPhase(p)
	covered := make(map[string]bool, len(specs))
	for _, s := range specs {
		factory, ok := factories[s.Name]
		if !ok {
			return fmt.Errorf("rpc: verb %q (phase %d) has a VerbSpec but no handler factory", s.Name, p)
		}
		covered[s.Name] = true
		if err := srv.Register(s.Name, factory()); err != nil {
			return err
		}
	}
	for verb := range factories {
		if !covered[verb] {
			return fmt.Errorf("rpc: handler factory for %q registered in phase %d, but no VerbSpec has that verb in this phase", verb, p)
		}
	}
	return nil
}

// registerLifecycleVerbs wires the PhaseLifecycle verbs — the M11+M12
// agent-lifecycle and container-filesystem verbs — onto the RPC server
// now that the spawn runtime exists. It supplies the per-verb handler
// factories and delegates iteration + ordering to registerPhase, which
// reads the verb set from rpc.VerbSpecs (PhaseLifecycle). The CA is the
// daemon's signing CA (rpc handler embeds it in every issued JWT).
//
// enqueue is the reconcile hint sink (the daemon's Reconciler). The
// desired-state verbs (spawn/kill/archive/restart) write the record and
// then enqueue a reconcile so the sole-actuator loop converges the agent
// promptly; a nil enqueue is acceptable (the periodic sweep is the
// backstop).
func (r *rpcRuntime) registerLifecycleVerbs(ca *authjwt.CA, spawnRT *spawnRuntime, enqueue handlers.ReconcileEnqueuer) error {
	spawnDeps := spawnRT.asSpawnDeps(r.chConn)
	spawnDeps.CA = ca
	spawnDeps.Enqueue = enqueue
	// Guardrail: prompt_agent's heartbeat-staleness guard (L1 of agent
	// lifecycle truth) is captured by value in NewPromptAgent's closure.
	if r.heartbeats == nil {
		log.Printf("sextantd: registerLifecycleVerbs: WARNING heartbeats cache is nil; prompt_agent L1 staleness guard will not fire (see daemon.go ordering)")
	}
	filesDeps := handlers.FilesDeps{
		Definitions:  spawnDeps.Definitions,
		Incarnations: spawnDeps.Incarnations,
		Containers:   spawnRT.containers,
	}
	factories := map[string]func() rpc.Handler{
		// Desired-state writers — they never touch the container runtime;
		// the reconciler (sole actuator) does. They enqueue a hint.
		rpc.VerbSpawnAgent: func() rpc.Handler { return handlers.NewSpawnAgent(spawnDeps) },
		rpc.VerbKillAgent: func() rpc.Handler {
			return handlers.NewKillAgent(handlers.KillDeps{
				Definitions:  spawnDeps.Definitions,
				Incarnations: spawnDeps.Incarnations,
				Containers:   spawnDeps.Containers,
				Enqueue:      enqueue,
			})
		},
		rpc.VerbArchiveAgent: func() rpc.Handler {
			return handlers.NewArchiveAgent(handlers.ArchiveDeps{
				Definitions:  spawnDeps.Definitions,
				Incarnations: spawnDeps.Incarnations,
				Containers:   spawnDeps.Containers,
				Volumes:      spawnDeps.Volumes,
				Enqueue:      enqueue,
			})
		},
		rpc.VerbPromptAgent: func() rpc.Handler {
			return handlers.NewPromptAgent(handlers.PromptDeps{
				Definitions: spawnDeps.Definitions,
				NATS:        r.nc,
				From: sextantproto.Address{
					Kind: sextantproto.AddressDaemon,
					ID:   "daemon",
				},
				Heartbeats:            r.heartbeats,
				HeartbeatStaleness:    r.heartbeatStaleness,
				HeartbeatStartupGrace: r.heartbeatStartupGrace,
			})
		},
		rpc.VerbRestartAgent: func() rpc.Handler {
			return handlers.NewRestartAgent(handlers.RestartDeps{
				Definitions:  spawnDeps.Definitions,
				Incarnations: spawnDeps.Incarnations,
				Containers:   spawnRT.containers,
				Enqueue:      enqueue,
			})
		},
		rpc.VerbReadFile:        func() rpc.Handler { return handlers.NewReadFile(filesDeps) },
		rpc.VerbListDir:         func() rpc.Handler { return handlers.NewListDir(filesDeps) },
		rpc.VerbStat:            func() rpc.Handler { return handlers.NewStat(filesDeps) },
		rpc.VerbExecInContainer: func() rpc.Handler { return handlers.NewExecInContainer(filesDeps) },
	}
	return registerPhase(r.server, rpc.PhaseLifecycle, factories)
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

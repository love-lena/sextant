package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/love-lena/sextant-initial/pkg/authjwt"
	"github.com/love-lena/sextant-initial/pkg/clickhouseboot"
	"github.com/love-lena/sextant-initial/pkg/natsboot"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/supervisor"
	"github.com/love-lena/sextant-initial/pkg/version"
)

// daemon owns the lifecycle of one sextantd process. Subprocesses are
// driven by pkg/supervisor: NATS and ClickHouse each get their own
// Supervisor, which restarts the process with exponential backoff and
// quarantines after a configurable threshold.
type daemon struct {
	cfg sextantd.Config
	ca  *authjwt.CA

	// frozen ports — populated by the first successful Start; reused on
	// every subsequent restart so reconnecting clients keep the same
	// endpoint and runtime.json never lies.
	natsPort           int
	clickhousePort     int
	clickhouseHTTPPort int

	natsBaseCfg natsboot.Config
	chBaseCfg   clickhouseboot.Config

	// Current subprocess handles. Replaced on each supervisor restart.
	mu  sync.Mutex
	nc  *natsServerHandle
	chh *clickhouseServerHandle

	natsSup *supervisor.Supervisor
	chSup   *supervisor.Supervisor

	listener     *net.UnixListener
	socketCloser sync.Once

	startedAt time.Time

	supCtx    context.Context
	supCancel context.CancelFunc

	wg          sync.WaitGroup
	supExitMu   sync.Mutex
	supExitErrs []error
	supDone     chan struct{}
	supDoneOnce sync.Once

	// rpcRT is the live RPC server runtime. Set after startRPC; cleared
	// after stopRPC. Held under mu like the subprocess handles.
	rpcRT *rpcRuntime

	// mcpRT is the live MCP server runtime. Set after startMCP; cleared
	// in doShutdown.
	mcpRT *mcpRuntime

	// spawnRT bundles the agent-spawn-flow dependencies (containermgr,
	// KV buckets, etc.) shared by RPC + MCP. Set after the RPC server is
	// up (we need the KV handle the RPC opened) and torn down in
	// doShutdown.
	spawnRT *spawnRuntime

	// worktreeRT is the M14 worktree manager + KV handles. nil when
	// worktree.repo_root is unset in the config; the daemon still boots
	// in that case, just without the worktree surface.
	worktreeRT *worktreeRuntime

	shutdownOnce sync.Once
	shutdownErr  error
}

// natsServerHandle is the live nats subprocess + its connection details.
type natsServerHandle struct {
	srv *natsboot.Server
}

type clickhouseServerHandle struct {
	srv *clickhouseboot.Server
}

func newDaemon(cfg sextantd.Config) (*daemon, error) {
	ca, err := authjwt.LoadCA(cfg.CA.KeyPath, cfg.CA.PubPath)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w (run `sextant init` first)", err)
	}
	d := &daemon{
		cfg:     cfg,
		ca:      ca,
		supDone: make(chan struct{}),
	}
	return d, nil
}

// Start runs the first-boot sequence per
// specs/components/sextantd.md §"Startup sequence":
//
//  1. Bring NATS up once; freeze the OS-allocated port.
//  2. Bring ClickHouse up once; freeze its ports; apply migrations.
//  3. Connect as operator + run JetStream bootstrap (idempotent).
//  4. Open the control socket.
//  5. Write runtime.json.
//  6. Hand both subprocesses to their supervisors. From here on,
//     restart-on-failure with backoff + quarantine is in effect.
//
// On any startup error, Start rolls back partial state.
func (d *daemon) Start(ctx context.Context) error {
	d.startedAt = time.Now().UTC()
	log.Printf("sextantd: starting (config=%s data=%s)",
		d.cfg.Paths.ConfigDir, d.cfg.Paths.DataDir)

	// 1. Build the NATS base config + start the server once.
	creds, err := sextantd.ReadOperatorCreds(d.cfg.NATS.OperatorCreds)
	if err != nil {
		return fmt.Errorf("operator creds: %w", err)
	}
	d.natsBaseCfg = natsboot.DefaultConfig(d.cfg.NATS.DataDir)
	d.natsBaseCfg.ListenHost = d.cfg.NATS.ListenHost
	d.natsBaseCfg.ListenPort = d.cfg.NATS.ListenPort
	d.natsBaseCfg.LogFile = d.cfg.NATS.LogFile
	d.natsBaseCfg.JWTCAPubPath = d.cfg.CA.PubPath
	d.natsBaseCfg.OperatorUser = creds.User
	d.natsBaseCfg.OperatorPassword = creds.Password

	natsSrv, err := d.startNATSOnce(ctx)
	if err != nil {
		return fmt.Errorf("nats start: %w", err)
	}
	d.setNATSHandle(natsSrv)

	// 2. ClickHouse base config + start.
	chPassword, err := sextantd.ReadPasswordFile(d.cfg.ClickHouse.PasswordFile)
	if err != nil {
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("clickhouse password: %w", err)
	}
	d.chBaseCfg = clickhouseboot.DefaultConfig(d.cfg.ClickHouse.DataDir)
	d.chBaseCfg.ListenHost = d.cfg.ClickHouse.ListenHost
	d.chBaseCfg.HTTPPort = d.cfg.ClickHouse.HTTPPort
	d.chBaseCfg.TCPPort = d.cfg.ClickHouse.TCPPort
	d.chBaseCfg.Database = d.cfg.ClickHouse.Database
	d.chBaseCfg.User = d.cfg.ClickHouse.User
	d.chBaseCfg.LogFile = d.cfg.ClickHouse.LogFile
	d.chBaseCfg.Password = chPassword

	chSrv, err := d.startClickHouseOnce(ctx)
	if err != nil {
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("clickhouse start: %w", err)
	}
	d.setClickHouseHandle(chSrv)

	// 3. Apply ClickHouse migrations (once per daemon process).
	if err := d.applyMigrations(ctx, chSrv); err != nil {
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("clickhouse apply migrations: %w", err)
	}

	// 4. JetStream bootstrap via operator connection.
	if err := d.bootstrapNATS(ctx, natsSrv); err != nil {
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("nats bootstrap: %w", err)
	}

	// 5. Control socket.
	if err := d.openControlSocket(); err != nil {
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("control socket: %w", err)
	}

	// 6. Runtime info.
	if err := d.writeRuntimeInfo(natsSrv, chSrv); err != nil {
		d.closeSocket()
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("write runtime info: %w", err)
	}

	// 7. Hand the running subprocesses to the supervisors. The
	// supervisor's first StartFn invocation reuses the already-running
	// subprocess via the "preStarted" hook so we don't double-start.
	d.supCtx, d.supCancel = context.WithCancel(context.Background())

	natsSup, err := d.buildNATSSupervisor(natsSrv)
	if err != nil {
		d.closeSocket()
		_ = sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile)
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return err
	}
	chSup, err := d.buildClickHouseSupervisor(chSrv)
	if err != nil {
		d.closeSocket()
		_ = sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile)
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return err
	}
	d.natsSup = natsSup
	d.chSup = chSup

	d.wg.Add(2)
	go d.runSupervisor("nats", natsSup)
	go d.runSupervisor("clickhouse", chSup)

	// Drain events for each supervisor onto the log.
	go d.drainEvents("nats", natsSup.Events())
	go d.drainEvents("clickhouse", chSup.Events())

	// 8. Bring the MCP server up before RPC: spec lists MCP at step 7,
	// RPC at step 8. MCP needs a live NATS + ClickHouse so we sequence
	// it after the supervisors are running. SpawnDeps is wired after
	// MCP starts (it needs the resolved MCP URL).
	mcpRT, err := d.startMCP(ctx)
	if err != nil {
		if d.supCancel != nil {
			d.supCancel()
		}
		d.closeSocket()
		_ = sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile)
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("start mcp: %w", err)
	}
	d.mu.Lock()
	d.mcpRT = mcpRT
	d.mu.Unlock()
	// Re-write runtime.json now that the MCP server is up — the spec's
	// downstream consumers (sidecars, doctor) read it to discover the
	// auto-picked HTTP port and the stdio socket path.
	if err := d.writeRuntimeInfo(natsSrv, chSrv); err != nil {
		log.Printf("sextantd: refresh runtime.json after mcp: %v", err)
	}

	// 9. Bring the RPC server up. Same NATS/ClickHouse precondition.
	// startRPC opens the agent_definitions KV handle which the spawn
	// runtime reuses, so we sequence it before buildSpawnRuntime.
	rpcRT, err := d.startRPC(ctx)
	if err != nil {
		// Roll back: tear MCP down, cancel the supervisors and tear
		// NATS/ClickHouse back down so Start's contract (clean state on
		// failure) holds.
		_ = mcpRT.stop(ctx)
		if d.supCancel != nil {
			d.supCancel()
		}
		d.closeSocket()
		_ = sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile)
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("start rpc: %w", err)
	}
	d.mu.Lock()
	d.rpcRT = rpcRT
	d.mu.Unlock()

	// 10. Build the spawn runtime. Reuse the RPC server's NATS conn
	// and definitions KV so we don't end up with two connections for
	// the same daemon.
	spawnRT, err := d.buildSpawnRuntime(ctx, rpcRT.nc, rpcRT.agentDefsKV)
	if err != nil {
		_ = rpcRT.stopRPC()
		_ = mcpRT.stop(ctx)
		if d.supCancel != nil {
			d.supCancel()
		}
		d.closeSocket()
		_ = sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile)
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("build spawn runtime: %w", err)
	}
	spawnRT.setMCPURL(mcpRT.server.HTTPAddr())

	// 11. Build the worktree runtime first so the spawn runtime can
	// carry it through to spawn_agent (which mounts /workspace as the
	// per-incarnation worktree when the template requests it).
	// Optional: when worktree.repo_root is empty the daemon skips
	// without erroring (M14 transitional state).
	worktreeRT, err := d.buildWorktreeRuntime(ctx, rpcRT.nc)
	if err != nil {
		_ = spawnRT.containers.Close()
		_ = rpcRT.stopRPC()
		_ = mcpRT.stop(ctx)
		if d.supCancel != nil {
			d.supCancel()
		}
		d.closeSocket()
		_ = sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile)
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("build worktree runtime: %w", err)
	}
	if worktreeRT != nil {
		spawnRT.setWorktree(worktreeRT.mgr)
	}
	d.mu.Lock()
	d.spawnRT = spawnRT
	d.worktreeRT = worktreeRT
	d.mu.Unlock()

	// 12. Register the lifecycle verbs on the RPC server now that the
	// spawn runtime exists. Also hand the same dep bag to the MCP
	// server so the agent path uses the same backend.
	if err := rpcRT.registerLifecycleVerbs(d.ca, spawnRT); err != nil {
		_ = spawnRT.containers.Close()
		_ = rpcRT.stopRPC()
		_ = mcpRT.stop(ctx)
		if d.supCancel != nil {
			d.supCancel()
		}
		d.closeSocket()
		_ = sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile)
		_ = d.stopClickHouseNow(ctx)
		_ = d.stopNATSNow(ctx)
		return fmt.Errorf("register lifecycle verbs: %w", err)
	}
	mcpRT.server.SetSpawnDeps(spawnRT.asMCPDeps(d.ca, rpcRT.chConn))

	// 13. Register the worktree verbs/tools.
	if worktreeRT != nil {
		if err := rpcRT.registerWorktreeVerbs(worktreeRT); err != nil {
			_ = spawnRT.containers.Close()
			_ = rpcRT.stopRPC()
			_ = mcpRT.stop(ctx)
			if d.supCancel != nil {
				d.supCancel()
			}
			d.closeSocket()
			_ = sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile)
			_ = d.stopClickHouseNow(ctx)
			_ = d.stopNATSNow(ctx)
			return fmt.Errorf("register worktree verbs: %w", err)
		}
		mcpRT.server.SetWorktree(worktreeRT.mgr)
	}

	log.Printf("sextantd: ready (nats=%s clickhouse=%s control=%s rpc=sextant.rpc.* mcp=%s)",
		natsSrv.Address(), chSrv.TCPAddress(), d.cfg.Daemon.ControlSocket, mcpRT.server.HTTPURL())
	return nil
}

// Wait blocks until both supervisors have returned. Returns a joined
// error if either quarantined; nil on graceful shutdown.
func (d *daemon) Wait() error {
	<-d.supDone
	d.supExitMu.Lock()
	defer d.supExitMu.Unlock()
	if len(d.supExitErrs) == 0 {
		return nil
	}
	return errors.Join(d.supExitErrs...)
}

// Shutdown stops both supervisors and tears down the control socket,
// runtime.json, and any still-running subprocess. Idempotent.
func (d *daemon) Shutdown() error {
	d.shutdownOnce.Do(func() {
		d.shutdownErr = d.doShutdown()
	})
	return d.shutdownErr
}

func (d *daemon) doShutdown() error {
	timeout := d.cfg.Daemon.ShutdownTimeout.AsDuration()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Stop running sidecar containers BEFORE we tear NATS / ClickHouse
	// down. The sidecar's last heartbeat goes through NATS; if we
	// killed NATS first, the sidecar's shutdown publish would dangle.
	// Best-effort: continue shutdown even if a container stop fails.
	d.stopRunningIncarnations(ctx)

	// Tear the RPC and MCP servers down next — both hold open
	// connections to NATS and ClickHouse, so they must drain before we
	// kill the subprocesses underneath them. MCP first because it has
	// its own listeners (HTTP + Unix socket) that must close before any
	// long-lived sidecar session can pin a goroutine on a NATS publish.
	var errs []error
	d.mu.Lock()
	mcpRT := d.mcpRT
	d.mcpRT = nil
	rpcRT := d.rpcRT
	d.rpcRT = nil
	spawnRT := d.spawnRT
	d.spawnRT = nil
	d.mu.Unlock()
	if mcpRT != nil {
		if err := mcpRT.stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("mcp stop: %w", err))
		}
	}
	if rpcRT != nil {
		if err := rpcRT.stopRPC(); err != nil {
			errs = append(errs, fmt.Errorf("rpc stop: %w", err))
		}
	}
	if spawnRT != nil && spawnRT.containers != nil {
		if err := spawnRT.containers.Close(); err != nil {
			errs = append(errs, fmt.Errorf("containermgr close: %w", err))
		}
	}

	// Tell the supervisors to stop, then cancel their root context.
	if d.chSup != nil {
		if err := d.chSup.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("clickhouse stop: %w", err))
		}
	}
	if d.natsSup != nil {
		if err := d.natsSup.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("nats stop: %w", err))
		}
	}
	if d.supCancel != nil {
		d.supCancel()
	}

	// Close the control socket + runtime file early so doctor's "daemon
	// up" probe falls back to the "not running" path immediately.
	d.closeSocket()
	if err := sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile); err != nil {
		log.Printf("sextantd: runtime.json: %v", err)
	}

	// Wait for the supervisor goroutines to drain. Bounded by ctx.
	doneCh := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(doneCh)
	}()
	select {
	case <-doneCh:
	case <-ctx.Done():
		errs = append(errs, fmt.Errorf("supervisors did not drain within %s", timeout))
	}

	// Belt-and-suspenders: ensure subprocesses are truly gone.
	if err := d.stopClickHouseNow(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := d.stopNATSNow(ctx); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// startNATSOnce runs the first NATS startup; subsequent restarts come
// from buildNATSSupervisor's StartFn. The OS-allocated port (if any)
// is frozen into d.natsPort here.
func (d *daemon) startNATSOnce(ctx context.Context) (*natsboot.Server, error) {
	cfg := d.natsBaseCfg
	if d.natsPort != 0 {
		cfg.ListenPort = d.natsPort
	}
	srv, err := natsboot.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if d.natsPort == 0 {
		// First start — capture the OS-allocated port.
		d.natsPort = portFromAddr(srv.Address())
		d.natsBaseCfg.ListenPort = d.natsPort
	}
	return srv, nil
}

func (d *daemon) startClickHouseOnce(ctx context.Context) (*clickhouseboot.Server, error) {
	cfg := d.chBaseCfg
	if d.clickhousePort != 0 {
		cfg.TCPPort = d.clickhousePort
	}
	if d.clickhouseHTTPPort != 0 {
		cfg.HTTPPort = d.clickhouseHTTPPort
	}
	srv, err := clickhouseboot.Start(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if d.clickhousePort == 0 {
		d.clickhousePort = portFromAddr(srv.TCPAddress())
		d.chBaseCfg.TCPPort = d.clickhousePort
	}
	if d.clickhouseHTTPPort == 0 {
		d.clickhouseHTTPPort = portFromAddr(srv.HTTPAddress())
		d.chBaseCfg.HTTPPort = d.clickhouseHTTPPort
	}
	return srv, nil
}

// applyMigrations runs the bookkeeping-tracked ClickHouse migrations.
// Safe to call repeatedly: clickhouseboot.Apply is idempotent on the
// SHA-tracked migration table. We re-apply on every restart so any
// schema drift between binary and data dir surfaces immediately.
func (d *daemon) applyMigrations(ctx context.Context, chSrv *clickhouseboot.Server) error {
	conn, err := chSrv.Open(ctx)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer conn.Close() //nolint:errcheck // best-effort close
	return clickhouseboot.Apply(ctx, conn)
}

func (d *daemon) bootstrapNATS(ctx context.Context, srv *natsboot.Server) error {
	nc, err := srv.Connect()
	if err != nil {
		return fmt.Errorf("operator connect: %w", err)
	}
	defer nc.Close()
	bootCtx, bootCancel := context.WithTimeout(ctx, 30*time.Second)
	defer bootCancel()
	return natsboot.Bootstrap(bootCtx, nc, d.natsBaseCfg.MaxBytesPerStream)
}

// buildNATSSupervisor wires a supervisor.Unit whose StartFn returns the
// already-running NATS subprocess on the first call and starts a fresh
// one on every restart. After each restart we re-run JetStream bootstrap
// (idempotent) so a NATS data-dir corruption surfaces immediately.
func (d *daemon) buildNATSSupervisor(initial *natsboot.Server) (*supervisor.Supervisor, error) {
	first := initial
	return supervisor.New(supervisor.Unit{
		Name: "nats",
		Policy: supervisor.Policy{
			InitialBackoff:  d.cfg.Daemon.RestartBackoffInitial.AsDuration(),
			MaxBackoff:      d.cfg.Daemon.RestartBackoffMax.AsDuration(),
			QuarantineAfter: d.cfg.Daemon.RestartQuarantineAfter,
			ResetAfter:      d.cfg.Daemon.RestartBackoffMax.AsDuration(),
		},
		Start: func(ctx context.Context) (supervisor.Process, error) {
			var srv *natsboot.Server
			restart := first == nil
			if first != nil {
				srv = first
				first = nil
			} else {
				log.Printf("sextantd: restarting nats")
				newSrv, err := d.startNATSOnce(ctx)
				if err != nil {
					return nil, fmt.Errorf("restart nats: %w", err)
				}
				srv = newSrv
				if err := d.bootstrapNATS(ctx, srv); err != nil {
					_ = srv.Stop(ctx)
					return nil, fmt.Errorf("re-bootstrap nats: %w", err)
				}
			}
			d.setNATSHandle(srv)
			if restart {
				d.refreshRuntimeInfo()
			}
			return newNATSProcess(srv), nil
		},
	})
}

func (d *daemon) buildClickHouseSupervisor(initial *clickhouseboot.Server) (*supervisor.Supervisor, error) {
	first := initial
	return supervisor.New(supervisor.Unit{
		Name: "clickhouse",
		Policy: supervisor.Policy{
			InitialBackoff:  d.cfg.Daemon.RestartBackoffInitial.AsDuration(),
			MaxBackoff:      d.cfg.Daemon.RestartBackoffMax.AsDuration(),
			QuarantineAfter: d.cfg.Daemon.RestartQuarantineAfter,
			ResetAfter:      d.cfg.Daemon.RestartBackoffMax.AsDuration(),
		},
		Start: func(ctx context.Context) (supervisor.Process, error) {
			var srv *clickhouseboot.Server
			restart := first == nil
			if first != nil {
				srv = first
				first = nil
			} else {
				log.Printf("sextantd: restarting clickhouse")
				newSrv, err := d.startClickHouseOnce(ctx)
				if err != nil {
					return nil, fmt.Errorf("restart clickhouse: %w", err)
				}
				srv = newSrv
				// Migrations are idempotent — re-apply on restart so
				// schema drift between binary and data dir surfaces
				// immediately.
				if err := d.applyMigrations(ctx, srv); err != nil {
					_ = srv.Stop(ctx)
					return nil, fmt.Errorf("re-apply migrations: %w", err)
				}
			}
			d.setClickHouseHandle(srv)
			if restart {
				d.refreshRuntimeInfo()
			}
			return newClickHouseProcess(srv), nil
		},
	})
}

// runSupervisor blocks until the supervisor.Run returns, then records
// any error and signals the daemon as done if both supervisors have
// finished.
func (d *daemon) runSupervisor(name string, sup *supervisor.Supervisor) {
	defer d.wg.Done()
	err := sup.Run(d.supCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("sextantd: supervisor %s returned: %v", name, err)
		d.supExitMu.Lock()
		d.supExitErrs = append(d.supExitErrs, fmt.Errorf("%s: %w", name, err))
		d.supExitMu.Unlock()
	}
	// If either supervisor returns with an error (i.e. quarantine), we
	// signal the daemon to shut down. A clean Stop() of a supervisor
	// also returns nil — that's the graceful path.
	if err != nil && !errors.Is(err, context.Canceled) {
		d.signalSupervisorsDone()
	}
	// Last one out signals done.
	d.wg.Add(0) // no-op; just for clarity
}

// signalSupervisorsDone closes supDone exactly once so daemon.Wait
// unblocks.
func (d *daemon) signalSupervisorsDone() {
	d.supDoneOnce.Do(func() { close(d.supDone) })
}

func (d *daemon) drainEvents(name string, ch <-chan supervisor.Event) {
	for ev := range ch {
		switch ev.Kind {
		case supervisor.EventStarted:
			log.Printf("sextantd: %s started (try=%d)", name, ev.Tries)
		case supervisor.EventExited:
			log.Printf("sextantd: %s exited (try=%d): %v", name, ev.Tries, ev.Err)
		case supervisor.EventRestarting:
			log.Printf("sextantd: %s restarting in %s (try=%d)", name, ev.Wait, ev.Tries)
		case supervisor.EventQuarantined:
			log.Printf("sextantd: %s QUARANTINED after %d failures: %v", name, ev.Tries, ev.Err)
		case supervisor.EventStopped:
			log.Printf("sextantd: %s stopped", name)
		}
	}
	// Channel closed = supervisor exited. Make sure the daemon knows.
	d.signalSupervisorsDone()
}

// writeRuntimeInfo persists the live ports + PIDs for downstream tools.
// Called on first boot and after each supervised restart.
func (d *daemon) writeRuntimeInfo(natsSrv *natsboot.Server, chSrv *clickhouseboot.Server) error {
	rt := sextantd.RuntimeInfo{
		PID:            os.Getpid(),
		StartedAt:      d.startedAt,
		NATSAddr:       natsSrv.Address(),
		NATSPID:        natsSrv.PID(),
		ClickHouseTCP:  chSrv.TCPAddress(),
		ClickHouseHTTP: chSrv.HTTPAddress(),
		ClickHousePID:  chSrv.PID(),
		ControlSocket:  d.cfg.Daemon.ControlSocket,
		Version:        version.Version,
	}
	d.mu.Lock()
	if d.mcpRT != nil && d.mcpRT.server != nil {
		rt.MCPHTTPAddr = d.mcpRT.server.HTTPAddr()
		rt.MCPStdioSocket = d.mcpRT.server.StdioSocketPath()
	}
	d.mu.Unlock()
	return sextantd.WriteRuntimeInfo(d.cfg.Paths.RuntimeFile, rt)
}

// refreshRuntimeInfo re-writes runtime.json with the current subprocess
// handles. Called after each supervisor restart. Best-effort: a write
// failure logs but does not abort the daemon.
func (d *daemon) refreshRuntimeInfo() {
	natsSrv := d.currentNATS()
	chSrv := d.currentClickHouse()
	if natsSrv == nil || chSrv == nil {
		return
	}
	if err := d.writeRuntimeInfo(natsSrv, chSrv); err != nil {
		log.Printf("sextantd: refresh runtime.json: %v", err)
	}
}

func (d *daemon) setNATSHandle(srv *natsboot.Server) {
	d.mu.Lock()
	d.nc = &natsServerHandle{srv: srv}
	d.mu.Unlock()
}

func (d *daemon) setClickHouseHandle(srv *clickhouseboot.Server) {
	d.mu.Lock()
	d.chh = &clickhouseServerHandle{srv: srv}
	d.mu.Unlock()
}

func (d *daemon) currentNATS() *natsboot.Server {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.nc == nil {
		return nil
	}
	return d.nc.srv
}

func (d *daemon) currentClickHouse() *clickhouseboot.Server {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.chh == nil {
		return nil
	}
	return d.chh.srv
}

// stopNATSNow is the unconditional-stop path (used by Shutdown). The
// supervisor's regular Stop path is preferred; this is the safety net.
func (d *daemon) stopNATSNow(ctx context.Context) error {
	srv := d.currentNATS()
	if srv == nil {
		return nil
	}
	err := srv.Stop(ctx)
	d.mu.Lock()
	d.nc = nil
	d.mu.Unlock()
	return err
}

func (d *daemon) stopClickHouseNow(ctx context.Context) error {
	srv := d.currentClickHouse()
	if srv == nil {
		return nil
	}
	err := srv.Stop(ctx)
	d.mu.Lock()
	d.chh = nil
	d.mu.Unlock()
	return err
}

// openControlSocket binds the Unix socket at cfg.Daemon.ControlSocket
// with mode 0600 and starts an accept loop.
func (d *daemon) openControlSocket() error {
	path := d.cfg.Daemon.ControlSocket
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir control socket parent: %w", err)
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	}
	addr, err := net.ResolveUnixAddr("unix", path)
	if err != nil {
		return fmt.Errorf("resolve unix addr: %w", err)
	}
	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return fmt.Errorf("chmod control socket %s: %w", path, err)
	}
	d.listener = l
	go d.acceptLoop()
	return nil
}

func (d *daemon) closeSocket() {
	d.socketCloser.Do(func() {
		if d.listener != nil {
			_ = d.listener.Close()
		}
		if d.cfg.Daemon.ControlSocket != "" {
			if err := os.Remove(d.cfg.Daemon.ControlSocket); err != nil && !os.IsNotExist(err) {
				log.Printf("sextantd: remove socket: %v", err)
			}
		}
	})
}

func (d *daemon) acceptLoop() {
	greeting := []byte(fmt.Sprintf("OK sextantd/%s\n", version.Version))
	for {
		c, err := d.listener.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("sextantd: accept: %v", err)
			return
		}
		go func(conn *net.UnixConn) {
			defer conn.Close() //nolint:errcheck // best-effort close
			_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write(greeting); err != nil {
				log.Printf("sextantd: control write: %v", err)
			}
		}(c)
	}
}

// portFromAddr extracts the numeric port from a host:port string. Used
// to freeze OS-allocated ports between restarts. Returns 0 on parse
// failure, which falls back to the OS allocating a fresh port on the
// next start (and runtime.json will be re-written accordingly).
func portFromAddr(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	var p int
	if _, err := fmt.Sscanf(portStr, "%d", &p); err != nil {
		return 0
	}
	return p
}

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
	"github.com/love-lena/sextant-initial/pkg/version"
)

// daemon owns the supervisor lifecycle for one sextantd process.
type daemon struct {
	cfg sextantd.Config
	ca  *authjwt.CA

	nats *natsboot.Server
	ch   *clickhouseboot.Server

	listener     *net.UnixListener
	socketCloser sync.Once
	listenerErr  chan error

	startedAt time.Time

	mu          sync.Mutex
	done        chan struct{}
	stopErr     error
	shutdownRun bool
}

func newDaemon(cfg sextantd.Config) (*daemon, error) {
	ca, err := authjwt.LoadCA(cfg.CA.KeyPath, cfg.CA.PubPath)
	if err != nil {
		return nil, fmt.Errorf("load CA: %w (run `sextant init` first)", err)
	}
	return &daemon{
		cfg:         cfg,
		ca:          ca,
		listenerErr: make(chan error, 1),
		done:        make(chan struct{}),
	}, nil
}

// Start runs the daemon's startup sequence per
// specs/components/sextantd.md §"Startup sequence". On any error it
// rolls back partially-started subsystems before returning.
func (d *daemon) Start(ctx context.Context) error {
	d.startedAt = time.Now().UTC()
	log.Printf("sextantd: starting (config=%s data=%s)",
		d.cfg.Paths.ConfigDir, d.cfg.Paths.DataDir)

	// 1. NATS.
	natsCfg := natsboot.DefaultConfig(d.cfg.NATS.DataDir)
	natsCfg.ListenHost = d.cfg.NATS.ListenHost
	natsCfg.ListenPort = d.cfg.NATS.ListenPort
	natsCfg.LogFile = d.cfg.NATS.LogFile
	natsCfg.JWTCAPubPath = d.cfg.CA.PubPath
	// Use the password from operator.creds so the CLI and the daemon
	// share a credential.
	creds, err := sextantd.ReadOperatorCreds(d.cfg.NATS.OperatorCreds)
	if err != nil {
		return fmt.Errorf("operator creds: %w", err)
	}
	natsCfg.OperatorUser = creds.User
	natsCfg.OperatorPassword = creds.Password

	natsSrv, err := natsboot.Start(ctx, natsCfg)
	if err != nil {
		return fmt.Errorf("nats start: %w", err)
	}
	d.nats = natsSrv

	// 2. ClickHouse.
	chCfg := clickhouseboot.DefaultConfig(d.cfg.ClickHouse.DataDir)
	chCfg.ListenHost = d.cfg.ClickHouse.ListenHost
	chCfg.HTTPPort = d.cfg.ClickHouse.HTTPPort
	chCfg.TCPPort = d.cfg.ClickHouse.TCPPort
	chCfg.Database = d.cfg.ClickHouse.Database
	chCfg.User = d.cfg.ClickHouse.User
	chCfg.LogFile = d.cfg.ClickHouse.LogFile
	chPassword, err := sextantd.ReadPasswordFile(d.cfg.ClickHouse.PasswordFile)
	if err != nil {
		_ = d.stopNATS(ctx)
		return fmt.Errorf("clickhouse password: %w", err)
	}
	chCfg.Password = chPassword

	chSrv, err := clickhouseboot.Start(ctx, chCfg)
	if err != nil {
		_ = d.stopNATS(ctx)
		return fmt.Errorf("clickhouse start: %w", err)
	}
	d.ch = chSrv

	// 3. Apply ClickHouse migrations.
	conn, err := chSrv.Open(ctx)
	if err != nil {
		_ = d.stopClickHouse(ctx)
		_ = d.stopNATS(ctx)
		return fmt.Errorf("clickhouse open: %w", err)
	}
	if err := clickhouseboot.Apply(ctx, conn); err != nil {
		_ = conn.Close()
		_ = d.stopClickHouse(ctx)
		_ = d.stopNATS(ctx)
		return fmt.Errorf("clickhouse apply migrations: %w", err)
	}
	_ = conn.Close()

	// 4. Operator NATS connection + JetStream bootstrap.
	nc, err := natsSrv.Connect()
	if err != nil {
		_ = d.stopClickHouse(ctx)
		_ = d.stopNATS(ctx)
		return fmt.Errorf("operator connect: %w", err)
	}
	bootCtx, bootCancel := context.WithTimeout(ctx, 30*time.Second)
	bootErr := natsboot.Bootstrap(bootCtx, nc, natsCfg.MaxBytesPerStream)
	bootCancel()
	nc.Close()
	if bootErr != nil {
		_ = d.stopClickHouse(ctx)
		_ = d.stopNATS(ctx)
		return fmt.Errorf("nats bootstrap: %w", bootErr)
	}

	// 5. Control socket.
	if err := d.openControlSocket(); err != nil {
		_ = d.stopClickHouse(ctx)
		_ = d.stopNATS(ctx)
		return fmt.Errorf("control socket: %w", err)
	}

	// 6. Runtime info file.
	rt := sextantd.RuntimeInfo{
		PID:            os.Getpid(),
		StartedAt:      d.startedAt,
		NATSAddr:       natsSrv.Address(),
		ClickHouseTCP:  chSrv.TCPAddress(),
		ClickHouseHTTP: chSrv.HTTPAddress(),
		ControlSocket:  d.cfg.Daemon.ControlSocket,
		Version:        version.Version,
	}
	if err := sextantd.WriteRuntimeInfo(d.cfg.Paths.RuntimeFile, rt); err != nil {
		d.closeSocket()
		_ = d.stopClickHouse(ctx)
		_ = d.stopNATS(ctx)
		return fmt.Errorf("write runtime info: %w", err)
	}

	log.Printf("sextantd: ready (nats=%s clickhouse=%s control=%s)",
		natsSrv.Address(), chSrv.TCPAddress(), d.cfg.Daemon.ControlSocket)
	return nil
}

// Wait blocks until either the daemon's context is canceled (caller's
// signal handler) or a subsystem fails terminally. Returns nil on
// graceful exit, or the underlying error otherwise.
//
// M5 supervises NATS and ClickHouse subprocesses via polling; the
// pkg/supervisor abstraction kicks in for M7+ where we need real
// quarantine logic. For M5 the daemon stays alive as long as both
// subprocesses do; if either exits unexpectedly, Wait returns.
func (d *daemon) Wait() error {
	<-d.done
	d.mu.Lock()
	err := d.stopErr
	d.mu.Unlock()
	return err
}

// signalExit marks the daemon as terminating with err and closes done
// exactly once.
func (d *daemon) signalExit(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopErr == nil {
		d.stopErr = err
	}
	select {
	case <-d.done:
	default:
		close(d.done)
	}
}

// Shutdown tears the daemon down in reverse startup order. Safe to call
// multiple times; the second call is a no-op.
func (d *daemon) Shutdown() error {
	d.mu.Lock()
	if d.shutdownRun {
		d.mu.Unlock()
		return nil
	}
	d.shutdownRun = true
	d.mu.Unlock()

	// Mark done so any Wait callers unblock if they haven't already.
	d.signalExit(nil)

	timeout := d.cfg.Daemon.ShutdownTimeout.AsDuration()
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Order: control socket, runtime.json, ClickHouse, NATS.
	d.closeSocket()
	if err := sextantd.RemoveRuntimeInfo(d.cfg.Paths.RuntimeFile); err != nil {
		log.Printf("sextantd: runtime.json: %v", err)
	}
	var errs []error
	if err := d.stopClickHouse(ctx); err != nil {
		errs = append(errs, fmt.Errorf("clickhouse: %w", err))
	}
	if err := d.stopNATS(ctx); err != nil {
		errs = append(errs, fmt.Errorf("nats: %w", err))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (d *daemon) stopNATS(ctx context.Context) error {
	if d.nats == nil {
		return nil
	}
	err := d.nats.Stop(ctx)
	d.nats = nil
	return err
}

func (d *daemon) stopClickHouse(ctx context.Context) error {
	if d.ch == nil {
		return nil
	}
	err := d.ch.Stop(ctx)
	d.ch = nil
	return err
}

// openControlSocket binds the Unix socket at cfg.Daemon.ControlSocket
// with mode 0600 and starts an accept loop. Each accepted connection
// gets the M5 greeting ("OK <version>\n") and is closed.
func (d *daemon) openControlSocket() error {
	path := d.cfg.Daemon.ControlSocket
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("mkdir control socket parent: %w", err)
	}
	// Remove a stale socket file if it exists.
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

// closeSocket closes the listener (exactly once) and removes the socket
// file. Safe to call before openControlSocket.
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
			// Listener closed during shutdown — normal.
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

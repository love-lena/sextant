package natsboot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

// Server represents a running nats-server subprocess.
//
// Construct via Start. Stop must be called to terminate the subprocess
// and release the data directory's lock file.
type Server struct {
	cfg     Config
	cmd     *exec.Cmd
	cfgPath string
	logFile *os.File

	// waitCh receives exactly one value: the error returned by
	// cmd.Wait(). Closed after the value is delivered. Drained by
	// whichever call observes the exit first (Stop or Done consumers).
	waitCh chan error
	// waitErr caches the exit error so multiple Done/Stop callers see
	// the same result. Protected by mu.
	waitErr  error
	waitDone chan struct{}
	mu       sync.Mutex
}

// Start writes a NATS config file derived from cfg, starts nats-server
// with -c <config>, and waits up to cfg.StartupTimeout for the listener
// to accept connections. On failure the subprocess (if any) is killed
// before returning.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	cfg, err := cfg.validateAndFill()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, fmt.Errorf("natsboot: mkdir data dir: %w", err)
	}

	cfgPath := filepath.Join(cfg.DataDir, "nats-server.conf")
	if err := writeConfigFile(cfgPath, cfg); err != nil {
		return nil, fmt.Errorf("natsboot: write config: %w", err)
	}

	args := []string{"-js", "-c", cfgPath}
	cmd := exec.CommandContext(ctx, cfg.NATSBinary, args...) //nolint:gosec // binary path is config-controlled

	var logFile *os.File
	switch {
	case cfg.LogFile != "":
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640) //nolint:gosec // operator-config controlled
		if err != nil {
			return nil, fmt.Errorf("natsboot: open log file %s: %w", cfg.LogFile, err)
		}
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	default:
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	// New process group so we can SIGINT the whole tree on Stop.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// exec.CommandContext's default Cancel callback calls
	// cmd.Process.Kill, which SIGKILLs only the leader pid. nats-server
	// currently has no children, but we mirror clickhouseboot here so
	// any future helper process in the same group can't slip through
	// the ctx-cancel path (the same leak vector 2903609 fixed for the
	// explicit Stop path).
	cmd.Cancel = func() error {
		return signalProcessGroup(cmd, syscall.SIGKILL)
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		return nil, fmt.Errorf("natsboot: start nats-server: %w", err)
	}

	s := &Server{
		cfg:      cfg,
		cmd:      cmd,
		cfgPath:  cfgPath,
		logFile:  logFile,
		waitCh:   make(chan error, 1),
		waitDone: make(chan struct{}),
	}
	// Single owner of cmd.Wait(). Result delivered via waitCh; consumers
	// (Stop, Done) observe via waitDone closure + waitErr field.
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.waitErr = err
		s.mu.Unlock()
		s.waitCh <- err
		close(s.waitDone)
	}()

	if err := s.waitReady(ctx); err != nil {
		// Use a detached cleanup context so the original ctx being
		// canceled does not abort the SIGINT-and-wait.
		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		_ = s.Stop(stopCtx) //nolint:contextcheck // intentional detach for cleanup
		stopCancel()
		return nil, err
	}
	return s, nil
}

// Address returns the host:port the server is listening on.
func (s *Server) Address() string {
	return net.JoinHostPort(s.cfg.ListenHost, strconv.Itoa(s.cfg.ListenPort))
}

// URL returns a NATS connection URL with embedded DAEMON credentials.
// The daemon principal is the privileged sole-publisher (feat-ctl-f0);
// this URL is for the daemon's own in-process use. Treat as sensitive —
// it carries the daemon password. Operators/CLIs use OperatorURL.
func (s *Server) URL() string {
	return fmt.Sprintf("nats://%s:%s@%s", s.cfg.DaemonUser, s.cfg.DaemonPassword, s.Address())
}

// OperatorURL returns a NATS connection URL with embedded OPERATOR
// credentials — the broker-scoped principal (publish sextant.rpc.* +
// reply inboxes only). Sensitive — carries the operator password.
func (s *Server) OperatorURL() string {
	return fmt.Sprintf("nats://%s:%s@%s", s.cfg.OperatorUser, s.cfg.OperatorPassword, s.Address())
}

// PublicURL returns the bare nats:// URL without credentials. Useful for
// telemetry and logs.
func (s *Server) PublicURL() string {
	return "nats://" + s.Address()
}

// OperatorUser returns the broker-scoped operator NATS username.
func (s *Server) OperatorUser() string { return s.cfg.OperatorUser }

// OperatorPassword returns the operator NATS password. Sensitive — log
// only at trace level if at all.
func (s *Server) OperatorPassword() string { return s.cfg.OperatorPassword }

// DaemonUser returns the privileged daemon NATS username (sole publisher).
func (s *Server) DaemonUser() string { return s.cfg.DaemonUser }

// DaemonPassword returns the daemon NATS password. Sensitive.
func (s *Server) DaemonPassword() string { return s.cfg.DaemonPassword }

// SidecarUser returns the broker-scoped sidecar NATS username (the
// principal forwarded into sidecar containers at spawn).
func (s *Server) SidecarUser() string { return s.cfg.SidecarUser }

// SidecarPassword returns the sidecar NATS password. Sensitive.
func (s *Server) SidecarPassword() string { return s.cfg.SidecarPassword }

// DataDir returns the JetStream data directory.
func (s *Server) DataDir() string { return s.cfg.DataDir }

// ConfigPath returns the rendered nats-server config path. Useful for
// tests and `sextant-natsboot` to surface in --help output.
func (s *Server) ConfigPath() string { return s.cfgPath }

// Connect opens a NATS connection authenticated as the privileged DAEMON
// user (feat-ctl-f0). This is the daemon's in-process connection — RPC,
// MCP, reconciler — and the broker-sanctioned sole publisher to agent
// inboxes. CLIs/TUIs must NOT use this; they connect with the scoped
// operator credentials via pkg/client. The caller owns nc.Close().
func (s *Server) Connect(opts ...nats.Option) (*nats.Conn, error) {
	allOpts := append([]nats.Option{
		nats.Name("sextant-natsboot"),
		nats.UserInfo(s.cfg.DaemonUser, s.cfg.DaemonPassword),
		nats.RetryOnFailedConnect(false),
		nats.Timeout(2 * time.Second),
		nats.MaxReconnects(0),
	}, opts...)
	nc, err := nats.Connect(s.Address(), allOpts...)
	if err != nil {
		return nil, fmt.Errorf("natsboot: connect as daemon: %w", err)
	}
	return nc, nil
}

// ConnectOperator opens a NATS connection authenticated as the
// broker-scoped operator user. Exposed for tests and tools that need to
// exercise the operator front door (e.g. assert the broker rejects an
// inbox publish from this principal). The caller owns nc.Close().
func (s *Server) ConnectOperator(opts ...nats.Option) (*nats.Conn, error) {
	allOpts := append([]nats.Option{
		nats.Name("sextant-natsboot-operator"),
		nats.UserInfo(s.cfg.OperatorUser, s.cfg.OperatorPassword),
		nats.RetryOnFailedConnect(false),
		nats.Timeout(2 * time.Second),
		nats.MaxReconnects(0),
	}, opts...)
	nc, err := nats.Connect(s.Address(), allOpts...)
	if err != nil {
		return nil, fmt.Errorf("natsboot: connect as operator: %w", err)
	}
	return nc, nil
}

// Stop signals the subprocess to exit, waits up to ShutdownTimeout, then
// SIGKILLs if it has not stopped. Closes the log file. Safe to call more
// than once. If the subprocess has already exited (e.g. observed via
// Done), Stop returns the cached exit status without re-signalling.
func (s *Server) Stop(ctx context.Context) error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

	// Fast path: subprocess already exited (observed elsewhere).
	select {
	case <-s.waitDone:
		s.closeLog()
		return classifyExit(s.cachedWaitErr())
	default:
	}

	// SIGINT the whole process group, not just the leader. nats-server
	// flushes JetStream state on SIGINT and removes its lock file. The
	// group signal also catches any helpers nats-server spawned in
	// the same group (currently none, but the helper guards against
	// future drift). Setpgid:true at Start time makes the leader its
	// own group leader; -pid means "process group" per POSIX.
	if err := signalProcessGroup(s.cmd, syscall.SIGINT); err != nil {
		return fmt.Errorf("natsboot: SIGINT pgroup: %w", err)
	}

	timeout := s.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	select {
	case <-s.waitDone:
		// Best-effort SIGKILL the group on the success path too —
		// Wait reaps the leader, but any worker that ignored SIGINT
		// would still be running. ESRCH is normal once everyone's
		// gone; signalProcessGroup folds it to nil.
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		s.closeLog()
		return classifyExit(s.cachedWaitErr())
	case <-time.After(timeout):
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		<-s.waitDone
		s.closeLog()
		return fmt.Errorf("natsboot: nats-server did not stop within %s; sent SIGKILL", timeout)
	case <-ctx.Done():
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		<-s.waitDone
		s.closeLog()
		return ctx.Err()
	}
}

// signalProcessGroup sends sig to every process in the process group
// led by cmd's leader. Requires Setpgid:true at Start time so the
// leader's PGID equals its PID; we then signal -PID per POSIX
// convention. ESRCH (group already gone) folds to nil.
func signalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	return nil
}

// Done returns a channel that closes when the underlying nats-server
// subprocess exits (for any reason: graceful Stop, crash, external
// kill). Use ExitErr after Done is observed to inspect the cause.
func (s *Server) Done() <-chan struct{} {
	return s.waitDone
}

// PID returns the underlying nats-server subprocess PID. Returns 0 if
// the subprocess has not been started. Exported so the supervising
// daemon can record the pid in observability/audit and so tests can
// signal the subprocess directly to exercise restart behavior.
func (s *Server) PID() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// ExitErr returns the error from cmd.Wait(). Only meaningful after
// Done has fired; returns nil if the subprocess is still running.
func (s *Server) ExitErr() error {
	select {
	case <-s.waitDone:
		return s.cachedWaitErr()
	default:
		return nil
	}
}

func (s *Server) cachedWaitErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waitErr
}

// classifyExit folds expected exit conditions (signal-induced exit
// codes from nats-server) into nil.
func classifyExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// nats-server returns 0 on SIGINT and a non-zero code on
		// SIGTERM / SIGKILL. Either is "subprocess exited because we
		// said so" from natsboot's perspective.
		return nil
	}
	return fmt.Errorf("natsboot: wait nats-server: %w", err)
}

func (s *Server) closeLog() {
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
}

// waitReady polls the configured address with NATS connect attempts
// until cfg.StartupTimeout. Returns nil once a connection succeeds.
func (s *Server) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(s.cfg.StartupTimeout)
	var lastErr error
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("natsboot: context canceled while waiting for ready: %w", ctx.Err())
		}
		// Bail early if the subprocess has already died — observe
		// waitDone (closed by the single Wait goroutine) rather than
		// poking at cmd.ProcessState, which would race against the
		// cmd.Wait() writer.
		select {
		case <-s.waitDone:
			return fmt.Errorf("natsboot: nats-server exited during startup: %w", s.cachedWaitErr())
		default:
		}
		nc, err := nats.Connect(
			s.Address(),
			nats.UserInfo(s.cfg.OperatorUser, s.cfg.OperatorPassword),
			nats.Timeout(500*time.Millisecond),
			nats.RetryOnFailedConnect(false),
			nats.MaxReconnects(0),
		)
		if err == nil {
			nc.Close()
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("natsboot: nats-server did not become connect-able within %s; last error: %w",
				s.cfg.StartupTimeout, lastErr)
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// writeConfigFile renders cfg to path with mode 0o600. Mode 0o600 because
// the file contains the operator password.
func writeConfigFile(path string, cfg Config) error {
	var buf bytes.Buffer
	if err := renderConfig(&buf, cfg); err != nil {
		return fmt.Errorf("render config: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

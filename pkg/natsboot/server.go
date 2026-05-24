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

// URL returns a NATS connection URL with embedded operator credentials.
// Treat this string as sensitive — it carries the operator password.
func (s *Server) URL() string {
	return fmt.Sprintf("nats://%s:%s@%s", s.cfg.OperatorUser, s.cfg.OperatorPassword, s.Address())
}

// PublicURL returns the bare nats:// URL without credentials. Useful for
// telemetry and logs.
func (s *Server) PublicURL() string {
	return "nats://" + s.Address()
}

// OperatorUser returns the NATS auth username configured at start time.
func (s *Server) OperatorUser() string { return s.cfg.OperatorUser }

// OperatorPassword returns the NATS auth password. Sensitive — log only
// at trace level if at all.
func (s *Server) OperatorPassword() string { return s.cfg.OperatorPassword }

// DataDir returns the JetStream data directory.
func (s *Server) DataDir() string { return s.cfg.DataDir }

// ConfigPath returns the rendered nats-server config path. Useful for
// tests and `sextant-natsboot` to surface in --help output.
func (s *Server) ConfigPath() string { return s.cfgPath }

// Connect opens a NATS connection authenticated as the operator user.
// The caller owns nc.Close().
func (s *Server) Connect(opts ...nats.Option) (*nats.Conn, error) {
	allOpts := append([]nats.Option{
		nats.Name("sextant-natsboot"),
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

	// SIGINT for graceful shutdown — NATS server flushes JetStream
	// state and removes its lock file when it sees it.
	if err := s.cmd.Process.Signal(syscall.SIGINT); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return fmt.Errorf("natsboot: SIGINT: %w", err)
	}

	timeout := s.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	select {
	case <-s.waitDone:
		s.closeLog()
		return classifyExit(s.cachedWaitErr())
	case <-time.After(timeout):
		_ = s.cmd.Process.Kill()
		<-s.waitDone
		s.closeLog()
		return fmt.Errorf("natsboot: nats-server did not stop within %s; sent SIGKILL", timeout)
	case <-ctx.Done():
		_ = s.cmd.Process.Kill()
		<-s.waitDone
		s.closeLog()
		return ctx.Err()
	}
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
		nc, err := nats.Connect(s.Address(),
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

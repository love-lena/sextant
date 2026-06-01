package shipperboot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Server represents a running sextant-shipper subprocess. Construct via
// Start; Stop terminates the whole process group.
type Server struct {
	cfg     Config
	cmd     *exec.Cmd
	logFile *os.File

	// waitDone closes after cmd.Wait() returns. waitErr caches the exit
	// status so multiple readers see the same value.
	waitDone chan struct{}
	mu       sync.Mutex
	waitErr  error
}

// Start exec's the sextant-shipper binary with --config and
// --runtime-file pointing at the daemon's paths, sets up a new process
// group so the whole tree can be signaled at shutdown, and watches
// cfg.StartupGrace for an early crash. The returned *Server's Done
// channel closes when the subprocess exits.
//
// On a startup-window crash, Start tears the subprocess down and
// returns the exit error so the supervisor counts it as a failed start.
func Start(ctx context.Context, cfg Config) (*Server, error) {
	cfg, err := cfg.validateAndFill()
	if err != nil {
		return nil, err
	}

	args := []string{"--config", cfg.ConfigPath, "--runtime-file", cfg.RuntimePath}
	cmd := exec.CommandContext(ctx, cfg.BinaryPath, args...) //nolint:gosec // operator-resolved binary

	var logFile *os.File
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640) //nolint:gosec // operator-config controlled
		if err != nil {
			return nil, fmt.Errorf("shipperboot: open log file %s: %w", cfg.LogFile, err)
		}
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	} else {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("shipperboot: open %s: %w", os.DevNull, err)
		}
		logFile = f
		cmd.Stdout = f
		cmd.Stderr = f
	}

	// New process group so we can SIGTERM/SIGKILL the whole tree on
	// Stop. sextant-shipper currently has no children, but matching the
	// natsboot/clickhouseboot pattern guards against drift and ensures
	// the daemon-ctx-cancel path (cmd.Cancel below) does the right
	// thing. On Linux this ALSO sets Pdeathsig=SIGKILL so a HARD-killed
	// daemon (SIGKILL, no Go cleanup) does not leak the shipper as a
	// PPID=1 orphan — the kernel reaps it (failed test runs were observed
	// leaving ~8 orphaned sextant-shippers).
	cmd.SysProcAttr = shipperProcAttr()
	cmd.Cancel = func() error {
		return signalProcessGroup(cmd, syscall.SIGKILL)
	}

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("shipperboot: start sextant-shipper: %w", err)
	}

	s := &Server{
		cfg:      cfg,
		cmd:      cmd,
		logFile:  logFile,
		waitDone: make(chan struct{}),
	}
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.waitErr = err
		s.mu.Unlock()
		close(s.waitDone)
	}()

	// Brief startup-grace window: if the shipper crashes immediately
	// (bad config, ClickHouse unreachable longer than its startup
	// timeout, etc.) we surface that to the supervisor as a failed
	// start rather than letting it look like a successful start +
	// instant exit.
	select {
	case <-s.waitDone:
		_ = logFile.Close()
		return nil, fmt.Errorf("shipperboot: sextant-shipper exited during startup grace: %w",
			s.cachedWaitErr())
	case <-time.After(cfg.StartupGrace):
		// Subprocess still alive after the grace window — call it good.
		return s, nil
	case <-ctx.Done():
		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer stopCancel()
		_ = s.Stop(stopCtx) //nolint:contextcheck // intentional detach for cleanup
		return nil, ctx.Err()
	}
}

// Stop SIGTERMs the process group, then escalates to SIGKILL after
// cfg.ShutdownTimeout. Idempotent.
func (s *Server) Stop(ctx context.Context) error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	// Fast path: subprocess already exited.
	select {
	case <-s.waitDone:
		s.closeLog()
		return classifyExit(s.cachedWaitErr())
	default:
	}

	if err := signalProcessGroup(s.cmd, syscall.SIGTERM); err != nil {
		return fmt.Errorf("shipperboot: SIGTERM pgroup: %w", err)
	}

	timeout := s.cfg.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case <-s.waitDone:
		// Best-effort SIGKILL on the group on the success path too —
		// reaps the leader, kills any straggler that ignored SIGTERM.
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		s.closeLog()
		return classifyExit(s.cachedWaitErr())
	case <-time.After(timeout):
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		<-s.waitDone
		s.closeLog()
		return fmt.Errorf("shipperboot: sextant-shipper did not stop within %s; sent SIGKILL", timeout)
	case <-ctx.Done():
		_ = signalProcessGroup(s.cmd, syscall.SIGKILL)
		<-s.waitDone
		s.closeLog()
		return ctx.Err()
	}
}

// Done returns a channel that closes when the underlying sextant-shipper
// subprocess exits.
func (s *Server) Done() <-chan struct{} { return s.waitDone }

// PID returns the subprocess pid, or 0 if the subprocess is not started.
func (s *Server) PID() int {
	if s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// ExitErr returns the cached cmd.Wait() error. Only meaningful after
// Done has closed; returns nil while the subprocess is still running.
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

func (s *Server) closeLog() {
	if s.logFile != nil {
		_ = s.logFile.Close()
		s.logFile = nil
	}
}

// signalProcessGroup mirrors natsboot/clickhouseboot: send sig to every
// process in the cmd leader's group. Requires Setpgid:true at Start
// time. ESRCH (already gone) and ErrProcessDone fold to nil.
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

// classifyExit folds signal-induced exit codes (SIGTERM/SIGINT/SIGKILL)
// into nil. The shipper exits non-zero on shipper.ErrBackpressure;
// callers that care about that signal can inspect ExitErr directly.
func classifyExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return fmt.Errorf("shipperboot: wait: %w", err)
}

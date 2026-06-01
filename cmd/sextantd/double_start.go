package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// defaultRestartWait bounds the graceful-shutdown wait when --restart is
// passed. The value matches DaemonConfig.ShutdownTimeout's default — a
// healthy daemon completes its teardown well within this window. Operators
// override via the SEXTANTD_RESTART_WAIT env var (test-only knob; not
// documented as a supported public surface).
const defaultRestartWait = 30 * time.Second

// errAlreadyRunning is returned by probeExistingDaemon when a healthy
// peer holds runtime.json and --restart was not passed. The caller's job
// is to translate this into an exit-0 (benign duplicate start) rather
// than a non-zero error.
var errAlreadyRunning = errors.New("sextantd already running")

// checkExistingDaemonOrExit runs the pre-startup probe described in
// slug:feat-daemon-lifecycle-ergonomics fix #4. If a healthy
// peer daemon owns runtime.json:
//
//   - restart=false: print a friendly "already running" message to stderr
//     and exit 0. This is the benign-duplicate case (operator typed
//     `sextantd` twice) and should not crash on a port-bind collision.
//   - restart=true: SIGTERM the peer, wait up to defaultRestartWait (or
//     the SEXTANTD_RESTART_WAIT env override) for runtime.json to vanish,
//     then proceed with normal startup. If the peer hangs, log to stderr
//     and exit 1.
//
// A stale runtime.json (file present, listed PID dead) is silently
// cleaned up and startup proceeds either way.
func checkExistingDaemonOrExit(cfg sextantd.Config, restart bool) {
	stderr := os.Stderr
	if err := probeExistingDaemon(cfg, restart, stderr, time.Now()); err != nil {
		if errors.Is(err, errAlreadyRunning) {
			os.Exit(0)
		}
		_, _ = fmt.Fprintf(stderr, "sextantd: %v\n", err)
		os.Exit(1)
	}
}

// probeExistingDaemon is the testable core of checkExistingDaemonOrExit.
// Returns errAlreadyRunning sentinel for the exit-0 path, a wrapped error
// for the exit-1 path, or nil to proceed with startup.
func probeExistingDaemon(cfg sextantd.Config, restart bool, stderr io.Writer, now time.Time) error {
	path := cfg.Paths.RuntimeFile
	info, err := sextantd.ReadRuntimeInfo(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		// A corrupt runtime.json is treated as stale: log + remove so the
		// daemon can boot rather than wedging the operator's machine.
		log.Printf("sextantd: runtime.json unreadable, removing: %v", err)
		_ = sextantd.RemoveRuntimeInfo(path)
		return nil
	}
	if info.PID <= 0 {
		log.Printf("sextantd: runtime.json missing PID, removing")
		_ = sextantd.RemoveRuntimeInfo(path)
		return nil
	}
	if !isProcessAlive(info.PID) {
		log.Printf("sextantd: stale runtime.json (pid %d not running), removing", info.PID)
		_ = sextantd.RemoveRuntimeInfo(path)
		return nil
	}

	uptime := uptimeFrom(info, path, now)

	if !restart {
		_, _ = fmt.Fprintf(stderr, "sextantd already running (pid %d, uptime %s) — use --restart to replace\n",
			info.PID, uptime.Round(time.Second))
		return errAlreadyRunning
	}

	wait := restartWait()
	_, _ = fmt.Fprintf(stderr, "sextantd: replacing existing daemon (pid %d, uptime %s), waiting up to %s for shutdown\n",
		info.PID, uptime.Round(time.Second), wait)

	proc, err := os.FindProcess(info.PID)
	if err != nil {
		return fmt.Errorf("--restart: find pid %d: %w", info.PID, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// ESRCH means the peer died between the liveness check above and
		// the signal — treat as a successful shutdown.
		if errors.Is(err, syscall.ESRCH) {
			_ = sextantd.RemoveRuntimeInfo(path)
			return nil
		}
		return fmt.Errorf("--restart: signal pid %d: %w", info.PID, err)
	}

	if err := waitForRuntimeRemoval(path, info.PID, wait); err != nil {
		return fmt.Errorf("--restart: pid %d did not shut down within %s: %w", info.PID, wait, err)
	}
	return nil
}

// isProcessAlive checks whether pid corresponds to a running process via
// Signal(0) — the POSIX existence probe. Returns true if the process
// exists (and we have permission to signal it).
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		// EPERM means the process exists but is owned by a different
		// user. From sextantd's perspective that's still a live peer —
		// we should not stomp on it.
		if errors.Is(err, syscall.EPERM) {
			return true
		}
		return false
	}
	return true
}

// uptimeFrom derives the wall-clock uptime from runtime.json. Prefers
// StartedAt when set; falls back to the file mtime so the operator sees
// a meaningful number even when an older daemon's runtime.json predates
// the StartedAt field.
func uptimeFrom(info sextantd.RuntimeInfo, path string, now time.Time) time.Duration {
	if !info.StartedAt.IsZero() {
		return now.Sub(info.StartedAt)
	}
	if st, err := os.Stat(path); err == nil {
		return now.Sub(st.ModTime())
	}
	return 0
}

// waitForRuntimeRemoval polls until runtime.json disappears, the PID
// dies, or timeout elapses. Polls every 200ms — fast enough that a
// graceful shutdown that drops the file in <1s feels instant.
func waitForRuntimeRemoval(path string, pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			_ = sextantd.RemoveRuntimeInfo(path)
			return nil
		}
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out")
}

// restartWait returns the graceful-shutdown bound for --restart. Honors
// SEXTANTD_RESTART_WAIT (parseable by time.ParseDuration) so tests can
// shrink the production 30s default without rebuilding the binary.
func restartWait() time.Duration {
	if raw := os.Getenv("SEXTANTD_RESTART_WAIT"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultRestartWait
}

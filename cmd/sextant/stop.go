package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// runStop implements `sextant stop`. Sends SIGTERM and waits for the
// daemon to remove runtime.json on its way out. Deliberately does NOT
// follow up with SIGKILL — if the daemon is wedged, the operator
// chooses the destructive next step. Idempotent: no daemon = success.
// Plan: plans/issues/feat-daemon-lifecycle-ergonomics.md (fix #3).
func runStop(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant stop", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configDir := fs.String("config-dir", "", "config directory (default ~/.config/sextant)")
	dataDir := fs.String("data-dir", "", "data directory (default ~/.local/share/sextant)")
	timeout := fs.Duration("timeout", 30*time.Second, "max wait for runtime.json to disappear")
	help := fs.Bool("help", false, "print help")
	if err := fs.Parse(args); err != nil {
		return errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	if *help {
		fmt.Println(stopUsage)
		return nil
	}

	cfg, err := loadDaemonConfig(*configDir, *dataDir)
	if err != nil {
		return err
	}
	return doStop(os.Stdout, cfg, *timeout)
}

const stopUsage = `usage: sextant stop [--config-dir DIR] [--data-dir DIR] [--timeout 30s]

Sends SIGTERM to the daemon recorded in runtime.json and waits for
graceful shutdown. Does not escalate to SIGKILL — let the operator make
that call. Idempotent: prints "daemon not running" and exits 0 when no
daemon is recorded.`

// doStop is the testable body of `sextant stop`.
func doStop(w io.Writer, cfg sextantd.Config, timeout time.Duration) error {
	st, err := readDaemonState(cfg.Paths.RuntimeFile)
	switch {
	case errors.Is(err, errDaemonNotRunning):
		printf(w, "daemon not running\n")
		return nil
	case err != nil:
		return fmt.Errorf("read runtime.json: %w", err)
	}
	if !st.Alive {
		// runtime.json points at a dead PID. Clean it up and report
		// success — the goal state ("nothing is running") already
		// holds.
		if rmErr := sextantd.RemoveRuntimeInfo(cfg.Paths.RuntimeFile); rmErr != nil {
			return fmt.Errorf("remove stale runtime.json: %w", rmErr)
		}
		printf(w, "daemon not running (cleared stale runtime.json pid %d)\n", st.Info.PID)
		return nil
	}

	pid := st.Info.PID
	printf(w, "stopping daemon (pid %d)\n", pid)
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", pid, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("SIGTERM pid %d: %w", pid, err)
	}
	// The daemon removes runtime.json as part of its shutdown sequence
	// (see cmd/sextantd/daemon.go). Its absence is the canonical signal
	// the daemon has handed off — we don't probe the PID directly
	// because PIDs can race against recycling, while file-absence is
	// monotonic.
	if err := waitForDaemonGone(cfg.Paths.RuntimeFile, timeout); err != nil {
		printf(w, "warning: %v — daemon may still be shutting down\n", err)
		return fmt.Errorf("daemon did not shut down within %s", timeout)
	}
	printf(w, "daemon stopped (was pid %d)\n", pid)
	return nil
}

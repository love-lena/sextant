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

// doStop is the testable body of `sextant stop`. After handling the
// runtime.json daemon (if any), also scans for orphan sextantd
// processes — ones whose runtime.json was lost but whose process kept
// running — and SIGTERMs them. The goal state of `sextant stop` is
// "no sextantd is running"; without the orphan sweep that goal would
// silently NOT hold in the very situation the operator most needs
// cleanup. Discovered live in 2026-05-25 ops session.
func doStop(w io.Writer, cfg sextantd.Config, timeout time.Duration) error {
	st, err := readDaemonState(cfg.Paths.RuntimeFile)
	var trackedPID int
	switch {
	case errors.Is(err, errDaemonNotRunning):
		// No runtime.json — fall through to the orphan sweep. We still
		// need to clean up zombies.
	case err != nil:
		return fmt.Errorf("read runtime.json: %w", err)
	case !st.Alive:
		// runtime.json points at a dead PID. Clear the file so callers
		// downstream don't trip on it. Orphan sweep still runs.
		if rmErr := sextantd.RemoveRuntimeInfo(cfg.Paths.RuntimeFile); rmErr != nil {
			return fmt.Errorf("remove stale runtime.json: %w", rmErr)
		}
		printf(w, "cleared stale runtime.json (pid %d was not running)\n", st.Info.PID)
	default:
		trackedPID = st.Info.PID
		printf(w, "stopping daemon (pid %d)\n", trackedPID)
		proc, err := os.FindProcess(trackedPID)
		if err != nil {
			return fmt.Errorf("find pid %d: %w", trackedPID, err)
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			return fmt.Errorf("SIGTERM pid %d: %w", trackedPID, err)
		}
		// The daemon removes runtime.json as part of its shutdown
		// sequence. Wait for that as the canonical handoff signal
		// (file-absence is monotonic; PID liveness can race recycling).
		if err := waitForDaemonGone(cfg.Paths.RuntimeFile, timeout); err != nil {
			printf(w, "warning: %v — daemon may still be shutting down\n", err)
			return fmt.Errorf("daemon did not shut down within %s", timeout)
		}
		printf(w, "daemon stopped (was pid %d)\n", trackedPID)
	}

	// Orphan sweep. Resolve the sextantd binary path so we can match by
	// full path (no false positives from unrelated binaries). Failure to
	// resolve isn't fatal — operators may have stopped a daemon whose
	// binary they then removed; just skip the sweep with a warning.
	bin, binErr := findSextantdBinary()
	if binErr != nil {
		printf(w, "warning: orphan scan skipped: %v\n", binErr)
		return nil
	}
	orphans, scanErr := findOrphanSextantd(bin, trackedPID)
	if scanErr != nil {
		printf(w, "warning: orphan scan failed: %v\n", scanErr)
		return nil
	}
	if len(orphans) == 0 {
		// Make absence visible only when there's an audience for it —
		// emitting a "no orphans" line every stop would be noise in the
		// 99% case. Print the line only when we also did real shutdown
		// work above, so scripts looking at stop output see a uniform
		// trailing summary on the path that's relevant.
		if trackedPID == 0 && (errors.Is(err, errDaemonNotRunning)) {
			printf(w, "daemon not running\n")
		}
		return nil
	}
	printf(w, "found %d orphan sextantd process(es) without runtime.json:\n", len(orphans))
	for _, pid := range orphans {
		printf(w, "  SIGTERM pid %d\n", pid)
	}
	stillAlive := sigtermPIDs(orphans, timeout)
	if len(stillAlive) > 0 {
		return fmt.Errorf("orphan sextantd did not exit within %s: pid %v (try `kill -9` manually)", timeout, stillAlive)
	}
	printf(w, "orphan sextantd cleaned up (%d process(es))\n", len(orphans))
	return nil
}

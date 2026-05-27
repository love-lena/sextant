// stop.go owns `doStop` — the testable body of `sextant daemon stop`.
// The cobra wiring lives in daemon.go.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// doStop is the testable body of `sextant daemon stop`. After handling
// the runtime.json daemon (if any), also scans for orphan sextantd
// processes — ones whose runtime.json was lost but whose process kept
// running — and SIGTERMs them. The goal state of `daemon stop` is
// "no sextantd is running"; without the orphan sweep that goal would
// silently NOT hold in the very situation the operator most needs
// cleanup.
func doStop(w io.Writer, cfg sextantd.Config, timeout time.Duration) error {
	st, err := readDaemonState(cfg.Paths.RuntimeFile)
	var trackedPID int
	switch {
	case errors.Is(err, errDaemonNotRunning):
		// No runtime.json — fall through to the orphan sweep.
	case err != nil:
		return fmt.Errorf("read runtime.json: %w", err)
	case !st.Alive:
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
		if err := waitForDaemonGone(cfg.Paths.RuntimeFile, timeout); err != nil {
			printf(w, "warning: %v — daemon may still be shutting down\n", err)
			return fmt.Errorf("daemon did not shut down within %s", timeout)
		}
		printf(w, "daemon stopped (was pid %d)\n", trackedPID)
	}

	// Orphan sweep.
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

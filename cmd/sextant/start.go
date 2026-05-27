// start.go owns `doStart` — the testable body of `sextant daemon
// start`. The cobra wiring lives in daemon.go. Helpers shared with
// stop / restart / status / logs live in daemon_lifecycle.go.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// doStart is the testable body of `sextant daemon start`. Keeps the
// work observable by writing all progress to w; tests assert against
// that stream instead of stdout.
//
// Detaches a fresh sextantd under a new session so closing the
// controlling terminal doesn't kill it, pipes stdout/stderr into the
// canonical log file, and waits up to `timeout` for runtime.json to
// appear with a live PID.
func doStart(w io.Writer, cfg sextantd.Config, timeout time.Duration) error {
	// 1. Is the daemon already up? If so, idempotent success.
	st, err := readDaemonState(cfg.Paths.RuntimeFile)
	switch {
	case err == nil && st.Alive:
		printf(w, "daemon already running (pid %d) — use sextant daemon restart to replace\n", st.Info.PID)
		return nil
	case err == nil && !st.Alive:
		if rmErr := sextantd.RemoveRuntimeInfo(cfg.Paths.RuntimeFile); rmErr != nil {
			return fmt.Errorf("remove stale runtime.json: %w", rmErr)
		}
		printf(w, "removed stale runtime.json (pid %d not running)\n", st.Info.PID)
	case errors.Is(err, errDaemonNotRunning):
		// Fresh start. Nothing to do.
	default:
		return fmt.Errorf("read runtime.json: %w", err)
	}

	// 2. Find sextantd.
	bin, err := findSextantdBinary()
	if err != nil {
		return err
	}

	// 2b. Zombie check.
	orphans, scanErr := findOrphanSextantd(bin, 0)
	if scanErr != nil {
		printf(w, "warning: orphan scan failed: %v (continuing)\n", scanErr)
	}
	if len(orphans) > 0 {
		printf(w, "found orphan sextantd process(es) without runtime.json:\n")
		for _, pid := range orphans {
			printf(w, "  pid %d  %s\n", pid, bin)
		}
		printf(w, "run `sextant daemon stop` to clean them up, then retry `sextant daemon start`.\n")
		return fmt.Errorf("orphan sextantd detected (pid %v)", orphans)
	}

	// 3. Open the log file.
	logPath := daemonLogPath(cfg.Paths.DataDir, sextantd.RuntimeInfo{})
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return fmt.Errorf("ensure log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // operator-config controlled path
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}
	defer func() { _ = logFile.Close() }()

	// 4. Spawn detached.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer func() { _ = devNull.Close() }()

	cfgPath := filepath.Join(cfg.Paths.ConfigDir, "sextantd.toml")
	cmdArgs := []string{}
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		cmdArgs = append(cmdArgs, "--config", cfgPath)
	}
	cmd := exec.Command(bin, cmdArgs...) //nolint:gosec // bin resolved by findSextantdBinary
	cmd.Stdin = devNull
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	printf(w, "starting %s%s\n", bin, formatArgs(cmdArgs))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start sextantd: %w", err)
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release sextantd process: %w", err)
	}

	// 5. Wait for runtime.json + live PID.
	final, err := waitForDaemonUp(cfg.Paths.RuntimeFile, timeout)
	if err != nil {
		lines, tailErr := tailLogLines(logPath, 50)
		if tailErr == nil && len(lines) > 0 {
			printf(w, "\n--- tail of %s ---\n", logPath)
			printLines(w, lines)
			printf(w, "--- end log ---\n")
		}
		return fmt.Errorf("daemon failed to start: %w", err)
	}

	printf(w, "daemon up (pid %d, log: %s)\n", final.Info.PID, logPath)
	return nil
}

// formatArgs returns a leading-space-prefixed echo of args (or "") so
// the "starting" line reads naturally with or without flags.
func formatArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	out := ""
	for _, a := range args {
		out += " " + a
	}
	return out
}

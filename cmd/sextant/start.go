package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// runStart implements `sextant start`. It detaches a fresh sextantd
// process under a new session (so closing the controlling terminal
// doesn't kill the daemon), pipes stdout/stderr into the canonical log
// file, and waits up to 30s for runtime.json to appear with a live PID.
// Plan: plans/issues/feat-daemon-lifecycle-ergonomics.md (fix #3).
func runStart(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant start", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configDir := fs.String("config-dir", "", "config directory (default ~/.config/sextant)")
	dataDir := fs.String("data-dir", "", "data directory (default ~/.local/share/sextant)")
	timeout := fs.Duration("timeout", 30*time.Second, "max wait for runtime.json to appear")
	help := fs.Bool("help", false, "print help")
	if err := fs.Parse(args); err != nil {
		return errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	if *help {
		fmt.Println(startUsage)
		return nil
	}

	cfg, err := loadDaemonConfig(*configDir, *dataDir)
	if err != nil {
		return err
	}
	return doStart(os.Stdout, cfg, *timeout)
}

const startUsage = `usage: sextant start [--config-dir DIR] [--data-dir DIR] [--timeout 30s]

Detaches a fresh sextantd process. The daemon runs in its own session
with stdout/stderr piped to ~/.local/share/sextant/sextantd.log.

Exit 0 if the daemon is already running. Exit 1 if startup times out
(the last 50 log lines are printed first so the failure mode is visible).`

// doStart is the testable body of `sextant start`. Keeps the work
// observable by writing all progress to w; tests assert against that
// stream instead of stdout.
func doStart(w io.Writer, cfg sextantd.Config, timeout time.Duration) error {
	// 1. Is the daemon already up? If so, idempotent success.
	st, err := readDaemonState(cfg.Paths.RuntimeFile)
	switch {
	case err == nil && st.Alive:
		printf(w, "daemon already running (pid %d) — use sextant restart to replace\n", st.Info.PID)
		return nil
	case err == nil && !st.Alive:
		// Stale runtime.json. Remove and continue. The next launch will
		// rewrite the file with a fresh PID; if we leave it in place
		// readDaemonState would briefly report Alive=true for the
		// already-dead PID once a process recycler reused it.
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

	// 2b. Zombie check: a sextantd matching `bin` might still be running
	// from a previous session where runtime.json was deleted (manually,
	// by a botched cleanup, etc.). Spawning a fresh daemon in that state
	// crashes on the ClickHouse data-dir lock, leaving two half-broken
	// processes. Refuse and point at `sextant stop`, which will clean
	// them up. Discovered live in 2026-05-25 ops session.
	orphans, scanErr := findOrphanSextantd(bin, 0)
	if scanErr != nil {
		// Scan failure shouldn't block startup outright — log a warning
		// and proceed. If a real orphan exists, the upcoming spawn will
		// still surface the conflict via clickhouse exit 76.
		printf(w, "warning: orphan scan failed: %v (continuing)\n", scanErr)
	}
	if len(orphans) > 0 {
		printf(w, "found orphan sextantd process(es) without runtime.json:\n")
		for _, pid := range orphans {
			printf(w, "  pid %d  %s\n", pid, bin)
		}
		printf(w, "run `sextant stop` to clean them up, then retry `sextant start`.\n")
		return fmt.Errorf("orphan sextantd detected (pid %v)", orphans)
	}

	// 3. Open the log file. We pre-create it so the spawn doesn't race
	//    a missing parent dir and so operators can `tail -f` it before
	//    the daemon has fully come up.
	logPath := daemonLogPath(cfg.Paths.DataDir, sextantd.RuntimeInfo{})
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return fmt.Errorf("ensure log dir: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // operator-config controlled path
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}
	defer func() { _ = logFile.Close() }()

	// 4. Spawn detached. Setsid puts the daemon in its own session so a
	//    HUP on the operator's TTY doesn't tear it down. Stdout/stderr
	//    go to the log file; stdin is /dev/null so the daemon can't try
	//    to read from the operator's terminal.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open /dev/null: %w", err)
	}
	defer func() { _ = devNull.Close() }()

	cfgPath := filepath.Join(cfg.Paths.ConfigDir, "sextantd.toml")
	cmdArgs := []string{}
	if _, statErr := os.Stat(cfgPath); statErr == nil {
		// Only pass --config when the file exists; otherwise the daemon
		// applies its own defaults (same fallback path loadDaemonConfig
		// takes above).
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
	// Release the child so its exit doesn't turn into a zombie we'd
	// have to reap. The wait happens via runtime.json / signal-0 probe.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release sextantd process: %w", err)
	}

	// 5. Wait for runtime.json + live PID.
	final, err := waitForDaemonUp(cfg.Paths.RuntimeFile, timeout)
	if err != nil {
		// Surface the log tail so the operator can diagnose without
		// hunting for the file path themselves.
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

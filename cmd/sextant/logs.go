package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// runLogs implements `sextant logs`. Default mode prints the last N
// lines and exits; --follow keeps polling for new bytes. We don't use
// inotify/fsnotify because the daemon log is append-only and the polling
// overhead is irrelevant compared to the operator's read time.
// Plan: plans/issues/feat-daemon-lifecycle-ergonomics.md (fix #3).
func runLogs(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("sextant logs", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configDir := fs.String("config-dir", "", "config directory (default ~/.config/sextant)")
	dataDir := fs.String("data-dir", "", "data directory (default ~/.local/share/sextant)")
	follow := fs.Bool("follow", false, "stream new bytes (like tail -f) until cancelled")
	tail := fs.Int("tail", 50, "number of trailing lines to print before following")
	help := fs.Bool("help", false, "print help")
	if err := fs.Parse(args); err != nil {
		return errUserUsage(fmt.Sprintf("parse flags: %v", err))
	}
	if *help {
		fmt.Println(logsUsage)
		return nil
	}
	if *tail < 0 {
		return errUserUsage("--tail must be >= 0")
	}

	cfg, err := loadDaemonConfig(*configDir, *dataDir)
	if err != nil {
		return err
	}
	// Resolve the log path from runtime.json first (so a daemon that
	// advertises a non-default path is honoured), otherwise fall back
	// to the conventional <DataDir>/sextantd.log.
	logPath := resolveLogPath(cfg)
	return doLogs(ctx, os.Stdout, logPath, *tail, *follow)
}

const logsUsage = `usage: sextant logs [--follow] [--tail N] [--config-dir DIR] [--data-dir DIR]

Reads the daemon log (~/.local/share/sextant/sextantd.log by default).
--tail N (default 50) prints the trailing N lines and exits unless
--follow is set, in which case the command streams new bytes until Ctrl+C.

Exit 1 if the log file does not exist.`

// resolveLogPath consults runtime.json first (so future LogFile fields
// take precedence) then falls back to the conventional path.
func resolveLogPath(cfg sextantd.Config) string {
	if st, err := readDaemonState(cfg.Paths.RuntimeFile); err == nil {
		return daemonLogPath(cfg.Paths.DataDir, st.Info)
	}
	return daemonLogPath(cfg.Paths.DataDir, sextantd.RuntimeInfo{})
}

// doLogs is the testable body. ctx aborts follow mode cleanly.
func doLogs(ctx context.Context, w io.Writer, path string, tail int, follow bool) error {
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("log file %s does not exist (daemon has never started?)", path)
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if st.IsDir() {
		return fmt.Errorf("%s is a directory, not a log file", path)
	}

	if tail > 0 {
		lines, err := tailLogLines(path, tail)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		printLines(w, lines)
	}
	if !follow {
		return nil
	}
	return followLog(ctx, w, path)
}

// followLog opens path and copies any new bytes to w until ctx is
// cancelled. We seek to the current end first so the trailing-N tail
// (already printed) isn't repeated. Implementation note: this is the
// simplest workable approach — open the file once, then poll for new
// bytes every 250ms. Log rotation isn't expected in M5 and not handled.
func followLog(ctx context.Context, w io.Writer, path string) error {
	f, err := os.Open(path) //nolint:gosec // operator-controlled path
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek end %s: %w", path, err)
	}
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read %s: %w", path, err)
		}
		// Poll with a tight ctx-aware sleep so SIGINT still feels
		// responsive.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(250 * time.Millisecond):
		}
	}
}

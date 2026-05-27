// logs.go owns `doLogs` — the testable body of `sextant daemon logs`.
// The cobra wiring lives in daemon.go.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

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
// cancelled.
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
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(250 * time.Millisecond):
		}
	}
}

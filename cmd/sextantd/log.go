package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// openLogFile opens the daemon's append-mode log sink and rewires the
// stdlib logger to tee every line into both stderr (so foreground
// operators keep their terminal feedback) and the file (so doctor and
// post-mortem debugging always have a canonical sink to point at).
//
// The path comes from cfg.Log.File — populated by sextantd.Config.Resolve
// to <data_dir>/sextantd.log when the operator didn't override it via
// [log] file = "..." in sextantd.toml. The parent directory is created
// with mode 0750 to match the existing data-dir layout; the log file
// itself is opened 0600 because operational details (subprocess pids,
// runtime paths, error contexts) may contain operator-sensitive info.
//
// Called by main() *before* any other startup work so the daemon's
// first logged line ("sextantd: starting…") is captured. The returned
// *os.File is closed on clean shutdown by main(); signal-driven exits
// rely on the OS to release the descriptor.
func openLogFile(cfg sextantd.Config) (*os.File, error) {
	path := cfg.Log.File
	if path == "" {
		return nil, fmt.Errorf("sextantd: log.file unset (Resolve should default it)")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("sextantd: mkdir log parent %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // operator-controlled path
	if err != nil {
		return nil, fmt.Errorf("sextantd: open log %s: %w", path, err)
	}
	// Tee stderr + file. Foreground operators see the same lines doctor
	// reads back from disk; nothing useful gets dropped when an upstream
	// systemd-style supervisor isn't capturing our stderr.
	log.SetOutput(io.MultiWriter(os.Stderr, f))
	return f, nil
}

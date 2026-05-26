package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// lifecycle_harness_test.go centralises the daemon-spawning setup used
// by start/stop/restart/status tests. The pattern mirrors cmd/sextantd's
// daemonHarness, but built bottom-up against the cmd/sextant package
// (we can't import cmd/sextantd's test helpers across packages).

// requireDaemonBins skips when the third-party processes the daemon
// supervises aren't on PATH. The lifecycle tests are integration tests
// — they only fail meaningfully when the full stack can come up.
func requireDaemonBins(t *testing.T) {
	t.Helper()
	for _, name := range []string{"nats-server", "clickhouse"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s not on PATH: %v", name, err)
		}
	}
}

// lifecycleHarness owns a tempdir-scoped config + data dir plus a built
// sextantd binary. Tests use it to ask `sextant start`/`stop`/etc. to
// drive an isolated daemon end-to-end.
type lifecycleHarness struct {
	cfg sextantd.Config
}

func newLifecycleHarness(t *testing.T) *lifecycleHarness {
	t.Helper()
	requireDaemonBins(t)

	// 1. Init dirs. Pre-fix #2 the daemon needs a usable install on
	//    disk (CA, operator creds, ch password, templates, sextantd.toml).
	//    The simplest way to get that is to call cmd/sextant's own init
	//    helper.
	opts := tempInitOpts(t)
	// macOS UDS path limit (~104 bytes) means we should keep the data
	// dir under /tmp not under t.TempDir() (which on macOS resolves to
	// /var/folders/...). Re-root data accordingly.
	shortData, err := os.MkdirTemp("", "sxd-lc")
	if err != nil {
		t.Fatalf("mkdir tmp data: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortData) })
	opts.DataDir = shortData
	if err := doInit(context.Background(), &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("doInit: %v", err)
	}

	// 2. Tighten the backoff knobs so any restart-on-failure tests
	//    don't wait on prod defaults.
	cfgPath := filepath.Join(opts.ConfigDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Daemon.RestartBackoffInitial = sextantd.Duration(100 * time.Millisecond)
	cfg.Daemon.RestartBackoffMax = sextantd.Duration(1 * time.Second)
	// MCP HTTP port 0 = kernel-picked; avoids back-to-back collisions.
	cfg.MCP.HTTPPort = 0
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// 3. Build sextantd next to a stub sextant binary. The stub doesn't
	//    actually exec sextant — but co-locating the binary mimics the
	//    `make install` layout that findSextantdBinary's sibling-lookup
	//    targets. We set SEXTANTD_BIN explicitly so the test doesn't
	//    rely on that lookup either.
	binDir := t.TempDir()
	sextantdBin := filepath.Join(binDir, "sextantd")
	build := exec.Command("go", "build", "-o", sextantdBin, "github.com/love-lena/sextant/cmd/sextantd") //nolint:gosec
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build sextantd: %v", err)
	}
	// Also build sextant-shipper so the daemon's auto-supervise path
	// has a sibling to find.
	shipperBin := filepath.Join(binDir, "sextant-shipper")
	buildShipper := exec.Command("go", "build", "-o", shipperBin, "github.com/love-lena/sextant/cmd/sextant-shipper") //nolint:gosec
	buildShipper.Stderr = os.Stderr
	if err := buildShipper.Run(); err != nil {
		t.Fatalf("go build sextant-shipper: %v", err)
	}

	t.Setenv("SEXTANTD_BIN", sextantdBin)

	return &lifecycleHarness{cfg: cfg}
}

// stopIfRunning sends SIGTERM (via doStop) on cleanup so a failed test
// doesn't leak a daemon.
func (h *lifecycleHarness) cleanup() {
	_ = doStop(&bytes.Buffer{}, h.cfg, 15*time.Second)
}

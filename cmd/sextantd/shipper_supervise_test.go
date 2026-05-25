package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant-initial/pkg/sextantd"
)

// TestSextantdSupervisesShipper is the acceptance test for
// plans/issues/feat-shipper-auto-supervise.md: with default config
// (auto_supervise=true), `pgrep sextant-shipper` must find a running
// shipper within 5s of the daemon's control socket coming up.
//
// The pgrep is scoped to the per-test daemon's shipper.toml path so
// the assertion is robust against any other sextant-shipper running
// on the host (e.g. an operator's standing instance).
func TestSextantdSupervisesShipper(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skipf("pgrep not on PATH: %v", err)
	}

	h := startDaemonHarness(t)

	// The daemon passes --config <config_dir>/shipper.toml to
	// sextant-shipper; pgrep -f on that path matches exactly the
	// subprocess this test's daemon spawned.
	shipperToml := filepath.Join(h.cfg.Paths.ConfigDir, "shipper.toml")

	deadline := time.Now().Add(5 * time.Second)
	var lastPids []int
	for {
		pids, err := pgrepF(shipperToml)
		if err != nil {
			t.Fatalf("pgrep: %v", err)
		}
		if len(pids) > 0 {
			lastPids = pids
			break
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("sextant-shipper not found via pgrep -f %q within 5s\n--- daemon log ---\n%s",
				shipperToml, h.tail(t))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("sextant-shipper running: pids=%v config=%s", lastPids, shipperToml)
}

// TestSextantdAutoSuperviseOff covers the operator-managed escape
// hatch: with [shipper] auto_supervise = false, the daemon must boot
// successfully and must NOT spawn a sextant-shipper child. The
// operator runs the shipper standalone (the legacy M6 behavior).
func TestSextantdAutoSuperviseOff(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skipf("pgrep not on PATH: %v", err)
	}

	// Build a harness, but flip auto_supervise=false in the on-disk
	// config before starting the daemon. We can't use the standard
	// startDaemonHarness wholesale because it rewrites sextantd.toml
	// itself; instead we lean on the same primitives.
	configDir, _ := runInitForTest(t)
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	off := false
	cfg.Shipper.AutoSupervise = &off
	// Same tightening as startDaemonHarness.
	cfg.Daemon.RestartBackoffInitial = sextantd.Duration(100 * time.Millisecond)
	cfg.Daemon.RestartBackoffMax = sextantd.Duration(1 * time.Second)
	cfg.MCP.HTTPPort = 0
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	h := startDaemonHarnessWithCfgPath(t, cfgPath)

	// Acceptance: after the daemon has been up long enough that the
	// shipper would have spawned, pgrep -f finds zero shippers for our
	// per-test shipper.toml.
	shipperToml := filepath.Join(h.cfg.Paths.ConfigDir, "shipper.toml")
	time.Sleep(2 * time.Second)
	pids, err := pgrepF(shipperToml)
	if err != nil {
		t.Fatalf("pgrep: %v", err)
	}
	if len(pids) != 0 {
		t.Fatalf("auto_supervise=false: expected 0 shippers, got pids=%v\n--- daemon log ---\n%s",
			pids, h.tail(t))
	}

	// And the daemon log should record the "operator must run …
	// standalone" line so the operator's expected behavior is
	// observable.
	if !strings.Contains(h.tail(t), "auto_supervise=false") {
		t.Errorf("expected daemon log to mention auto_supervise=false; tail:\n%s", h.tail(t))
	}
}

// pgrepF runs `pgrep -f <pattern>` and returns matched pids. Exit-1
// (no matches) folds to (nil, nil).
func pgrepF(pattern string) ([]int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pgrep", "-f", pattern).Output() //nolint:gosec // test-controlled pattern
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("pgrep -f %q: %w", pattern, err)
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, convErr := strconv.Atoi(line)
		if convErr != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids, nil
}

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// TestRestart_NotRunning_BringsDaemonUp covers the not-running-then-up
// branch: restart treats a missing runtime.json as "skip stop, go
// straight to start". Exercises the helper composition without
// needing a real spawn (we stub findSextantdBinary by setting
// SEXTANTD_BIN to a sentinel that doesn't actually have to come up).
func TestRestart_NotRunning_StopPhaseIsNoOp(t *testing.T) {
	// We can't easily exercise the full doStart path without nats-
	// server + clickhouse, so this test stops short of asserting
	// "daemon up" — it verifies the stop-phase no-op + the transition
	// log, then expects doStart to fail (because SEXTANTD_BIN is a
	// stub that doesn't actually become a daemon). That failure is
	// surfaced as an error from doRestart.
	opts := tempInitOpts(t)
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}

	// Point SEXTANTD_BIN at a stub that exits immediately. doStart will
	// fail when waiting for runtime.json, but the stop phase will have
	// already announced "daemon not running".
	dir := t.TempDir()
	stub := dir + "/sextantd-stub"
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil { //nolint:gosec
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("SEXTANTD_BIN", stub)

	var buf bytes.Buffer
	err := doRestart(&buf, cfg, 1*time.Second, 1*time.Second)
	// Expected: error from start phase (stub doesn't produce runtime.json).
	if err == nil {
		t.Fatalf("expected restart to fail when start stub doesn't write runtime.json; got nil")
	}
	out := buf.String()
	if !strings.Contains(out, "daemon not running") {
		t.Errorf("stop phase should announce not-running: %s", out)
	}
	if !strings.Contains(out, "starting…") {
		t.Errorf("transition line missing: %s", out)
	}
}

// TestRestart_RunningCycles is the full integration round-trip. The
// guard is that the PID changes across the restart — proves we
// actually killed the old daemon and spawned a new one.
func TestRestart_RunningCycles(t *testing.T) {
	h := newLifecycleHarness(t)
	defer h.cleanup()

	var startBuf bytes.Buffer
	if err := doStart(&startBuf, h.cfg, 45*time.Second); err != nil {
		t.Fatalf("first doStart: %v\nstdout:\n%s", err, startBuf.String())
	}
	first, err := sextantd.ReadRuntimeInfo(h.cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}

	var restartBuf bytes.Buffer
	if err := doRestart(&restartBuf, h.cfg,
		h.cfg.Daemon.ShutdownTimeout.AsDuration()+15*time.Second,
		45*time.Second,
	); err != nil {
		t.Fatalf("doRestart: %v\nstdout:\n%s", err, restartBuf.String())
	}
	out := restartBuf.String()
	if !strings.Contains(out, "stopping daemon") {
		t.Errorf("missing 'stopping daemon' in output: %s", out)
	}
	if !strings.Contains(out, "starting…") {
		t.Errorf("missing 'starting…' transition: %s", out)
	}
	second, err := sextantd.ReadRuntimeInfo(h.cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo post-restart: %v", err)
	}
	if second.PID == first.PID {
		t.Errorf("PID unchanged across restart (%d) — old daemon survived?", first.PID)
	}
	if !isProcessAlive(second.PID) {
		t.Errorf("post-restart pid %d not alive", second.PID)
	}
}

package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// TestStop_NotRunning_Idempotent asserts that `sextant stop` against
// an empty data dir succeeds and prints the "daemon not running"
// reminder. Idempotency is the contract — operators must be able to
// call stop in scripts without branching on prior state.
func TestStop_NotRunning_Idempotent(t *testing.T) {
	// Build a config rooted at a temp dir but without spawning a real
	// daemon. We skip the heavy harness because nothing needs nats-
	// server here.
	opts := tempInitOpts(t)
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}

	var buf bytes.Buffer
	if err := doStop(&buf, cfg, 5*time.Second); err != nil {
		t.Fatalf("doStop: %v", err)
	}
	if !strings.Contains(buf.String(), "daemon not running") {
		t.Errorf("expected 'daemon not running' in stdout: %q", buf.String())
	}
}

// TestStop_StaleRuntimeFile_Cleared covers the second idempotent path:
// runtime.json exists but the recorded pid is dead. We clear the file
// and report success. Important because crash-killing the daemon (or
// power loss) leaves the file behind.
func TestStop_StaleRuntimeFile_Cleared(t *testing.T) {
	opts := tempInitOpts(t)
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	stale := sextantd.RuntimeInfo{PID: 999_999, StartedAt: time.Now(), Version: "stale"}
	if err := sextantd.WriteRuntimeInfo(cfg.Paths.RuntimeFile, stale); err != nil {
		t.Fatalf("seed stale runtime.json: %v", err)
	}

	var buf bytes.Buffer
	if err := doStop(&buf, cfg, 5*time.Second); err != nil {
		t.Fatalf("doStop: %v", err)
	}
	if !strings.Contains(buf.String(), "cleared stale runtime.json") {
		t.Errorf("expected stale-cleared message: %q", buf.String())
	}
	if _, err := os.Stat(cfg.Paths.RuntimeFile); !os.IsNotExist(err) {
		t.Errorf("runtime.json still present after stale cleanup: err=%v", err)
	}
}

// TestStop_GracefullyShutsDown is the load-bearing integration test:
// start a real daemon, run stop, assert runtime.json disappears (the
// canonical contract — the daemon removes it on graceful shutdown).
// We avoid asserting on the PID directly because it can be recycled
// between our probe windows; runtime.json absence is what matters.
func TestStop_GracefullyShutsDown(t *testing.T) {
	h := newLifecycleHarness(t)

	var startBuf bytes.Buffer
	if err := doStart(&startBuf, h.cfg, 45*time.Second); err != nil {
		t.Fatalf("doStart: %v\nstdout:\n%s", err, startBuf.String())
	}
	rt, err := sextantd.ReadRuntimeInfo(h.cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	t.Logf("daemon started pid=%d", rt.PID)

	var stopBuf bytes.Buffer
	if err := doStop(&stopBuf, h.cfg, h.cfg.Daemon.ShutdownTimeout.AsDuration()+15*time.Second); err != nil {
		t.Fatalf("doStop: %v\nstdout:\n%s", err, stopBuf.String())
	}
	out := stopBuf.String()
	if !strings.Contains(out, "daemon stopped") {
		t.Errorf("missing success line: %s", out)
	}
	if _, err := os.Stat(h.cfg.Paths.RuntimeFile); !os.IsNotExist(err) {
		t.Errorf("runtime.json still present: err=%v", err)
	}
}

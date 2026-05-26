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
	// Point SEXTANTD_BIN at a stub so the orphan sweep can't accidentally
	// match a real sextantd elsewhere on the dev box.
	setStubSextantdBin(t)

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
	setStubSextantdBin(t)
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

// TestStop_CleansUpOrphanWithoutRuntimeJSON covers Lena's live
// scenario: no runtime.json on disk, but a sextantd process is still
// running. Plain `sextant stop` would have printed "daemon not running"
// and left the orphan in place; the operator was then stuck because
// `sextant start` couldn't take over. Fix: `sextant stop` always scans
// for orphans (by sextantd binary path) and SIGTERMs them too, so the
// goal state ("nothing is running") actually holds afterward.
func TestStop_CleansUpOrphanWithoutRuntimeJSON(t *testing.T) {
	opts := tempInitOpts(t)
	cfg := sextantd.DefaultConfig(opts.ConfigDir, opts.DataDir)
	if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}

	fakeDir := t.TempDir()
	fakeBin, fakePID, cleanupFake := spawnFakeSextantd(t, fakeDir)
	// Cleanup is a safety net — the test expects doStop to kill it.
	defer cleanupFake()
	t.Setenv("SEXTANTD_BIN", fakeBin)

	var buf bytes.Buffer
	if err := doStop(&buf, cfg, 10*time.Second); err != nil {
		t.Fatalf("doStop: %v\nstdout:\n%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "orphan") {
		t.Errorf("stdout should mention orphan cleanup:\n%s", out)
	}

	// The orphan must actually be gone — that's the goal state callers
	// rely on. Poll briefly in case the OS hasn't finished reaping.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(fakePID) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("orphan pid %d still alive after stop:\n%s", fakePID, out)
}

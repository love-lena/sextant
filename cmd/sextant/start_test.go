package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// TestStart_FromCleanState_BringsDaemonUp asserts the canonical happy
// path: an init'd config dir + a built sextantd binary + `sextant
// start` => runtime.json appears with a live PID and the success line
// is printed.
func TestStart_FromCleanState_BringsDaemonUp(t *testing.T) {
	h := newLifecycleHarness(t)
	defer h.cleanup()

	var buf bytes.Buffer
	if err := doStart(&buf, h.cfg, 45*time.Second); err != nil {
		t.Fatalf("doStart: %v\nstdout:\n%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "daemon up") {
		t.Errorf("stdout missing 'daemon up': %s", out)
	}

	rt, err := sextantd.ReadRuntimeInfo(h.cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	if !isProcessAlive(rt.PID) {
		t.Errorf("recorded pid %d not alive after start", rt.PID)
	}
	// Log file should exist post-start: doStart pre-creates the file
	// with mode 0600 before the spawn so operators can `tail -f` even
	// during boot.
	logPath := filepath.Join(h.cfg.Paths.DataDir, "sextantd.log")
	if st, err := os.Stat(logPath); err != nil {
		t.Errorf("log file missing: %v", err)
	} else if st.IsDir() {
		t.Errorf("log path is a directory")
	}
}

// TestStart_AlreadyRunning_ExitsZero asserts idempotency: a second
// `sextant start` while the daemon is already up prints the
// "already running" line and returns nil.
func TestStart_AlreadyRunning_ExitsZero(t *testing.T) {
	h := newLifecycleHarness(t)
	defer h.cleanup()

	var first bytes.Buffer
	if err := doStart(&first, h.cfg, 45*time.Second); err != nil {
		t.Fatalf("first doStart: %v\nstdout:\n%s", err, first.String())
	}
	rt, err := sextantd.ReadRuntimeInfo(h.cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}

	var second bytes.Buffer
	if err := doStart(&second, h.cfg, 5*time.Second); err != nil {
		t.Fatalf("second doStart: %v\nstdout:\n%s", err, second.String())
	}
	out := second.String()
	if !strings.Contains(out, "already running") {
		t.Errorf("second start missing 'already running': %s", out)
	}
	// PID must be unchanged — the second start MUST NOT spawn a new
	// daemon, that would defeat the idempotency claim.
	rt2, err := sextantd.ReadRuntimeInfo(h.cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo (post second start): %v", err)
	}
	if rt2.PID != rt.PID {
		t.Errorf("PID changed across idempotent start: %d -> %d", rt.PID, rt2.PID)
	}
}

// TestStart_StaleRuntimeFile_IsCleanedThenStarts proves the
// stale-cleanup branch: when runtime.json points at a dead pid we
// remove it and spawn fresh rather than refusing to start.
func TestStart_StaleRuntimeFile_IsCleanedThenStarts(t *testing.T) {
	h := newLifecycleHarness(t)
	defer h.cleanup()

	// Write a runtime.json with a PID we know is dead.
	stale := sextantd.RuntimeInfo{PID: 999_999, StartedAt: time.Now(), Version: "stale"}
	if err := sextantd.WriteRuntimeInfo(h.cfg.Paths.RuntimeFile, stale); err != nil {
		t.Fatalf("seed stale runtime.json: %v", err)
	}

	var buf bytes.Buffer
	if err := doStart(&buf, h.cfg, 45*time.Second); err != nil {
		t.Fatalf("doStart: %v\nstdout:\n%s", err, buf.String())
	}
	out := buf.String()
	if !strings.Contains(out, "removed stale runtime.json") {
		t.Errorf("stdout missing cleanup line: %s", out)
	}
	if !strings.Contains(out, "daemon up") {
		t.Errorf("stdout missing 'daemon up': %s", out)
	}
}

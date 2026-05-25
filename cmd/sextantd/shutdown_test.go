package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/love-lena/sextant-initial/pkg/sextantd"
)

// TestSextantdShutdownKillsClickHouse is the regression for
// plans/issues/bug-shutdown-orphan-clickhouse.md.
//
// Repro: start the daemon, SIGTERM it, then check `pgrep -f config.xml`.
// Before the fix, the clickhouse-server leader (and its watchdog child)
// outlived sextantd because main.go canceled the daemon ctx before
// driving the supervisor's graceful Stop path — exec.CommandContext's
// default Cancel callback SIGKILLed the leader pid only, orphaning the
// watchdog. After the fix, both die within shutdown_timeout.
//
// We scope pgrep to the per-test config.xml path so the assertion is
// robust against any other clickhouse instance running on the host.
func TestSextantdShutdownKillsClickHouse(t *testing.T) {
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skipf("pgrep not on PATH: %v", err)
	}

	h := startDaemonHarness(t)
	cfg := h.cfg

	rt, err := sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	if rt.ClickHousePID == 0 {
		t.Fatal("runtime.json missing ClickHousePID — daemon did not record subprocess pid")
	}

	// clickhouse server -C <data-dir>/config.xml — the rendered config
	// path is unique per test (t.TempDir-derived), so pgrep only
	// matches this daemon's subprocess(es). Robust against host-level
	// clickhouse instances.
	configXMLPath := filepath.Join(cfg.ClickHouse.DataDir, "config.xml")

	// Pre-condition: pgrep should find at least the leader.
	pre, err := pgrepFConfigXML(configXMLPath)
	if err != nil {
		t.Fatalf("pre-shutdown pgrep: %v", err)
	}
	if len(pre) == 0 {
		t.Fatalf("pre-shutdown pgrep -f %q found nothing; expected at least pid %d",
			configXMLPath, rt.ClickHousePID)
	}
	t.Logf("pre-shutdown clickhouse pids: %v (runtime.json leader=%d)", pre, rt.ClickHousePID)

	// Send SIGTERM — the bug repro signals SIGTERM specifically; the
	// existing roundtrip test covers SIGINT separately.
	if err := h.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("SIGTERM: %v", err)
	}

	shutdownTimeout := cfg.Daemon.ShutdownTimeout.AsDuration()

	// Wait for the daemon binary itself to exit so the harness Cleanup
	// is a no-op rather than racing with our assertion below.
	exitCh := make(chan error, 1)
	go func() { exitCh <- h.cmd.Wait() }()
	select {
	case <-exitCh:
	case <-time.After(shutdownTimeout + 5*time.Second):
		_ = h.cmd.Process.Kill()
		t.Fatalf("daemon did not exit within %s\n--- daemon log ---\n%s",
			shutdownTimeout+5*time.Second, h.tail(t))
	}

	// Acceptance: pgrep -f config.xml must return zero matches within
	// shutdown_timeout + 1s of SIGTERM. The daemon exit above already
	// burned some of that budget; we still poll up to the full
	// shutdown_timeout + 1s from this point to leave room for kernel
	// reaping latency.
	deadline := time.Now().Add(shutdownTimeout + 1*time.Second)
	var lastPids []int
	for {
		lastPids, err = pgrepFConfigXML(configXMLPath)
		if err != nil {
			t.Fatalf("post-shutdown pgrep: %v", err)
		}
		if len(lastPids) == 0 {
			t.Logf("clickhouse cleaned up; pgrep returned 0 matches")
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("clickhouse process(es) still alive %s after SIGTERM: %v\n--- daemon log ---\n%s",
		shutdownTimeout+1*time.Second, lastPids, h.tail(t))
}

// pgrepFConfigXML runs `pgrep -f <pattern>` and returns the matched
// pids. pgrep exits 1 with no matches — folded to (nil, nil) since
// "nothing matched" is our success case downstream.
func pgrepFConfigXML(pattern string) ([]int, error) {
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

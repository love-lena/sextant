package clickhouseboot

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestStopKillsEntireProcessTree is the regression guard for the
// orphan-subprocess class of bugs. clickhouse-server forks an internal
// worker that stays in the leader's process group; a pre-fix Stop()
// only signaled the single leader PID, leaving the worker as a PPID=1
// orphan.
//
// The test snapshots every PID in the leader's group before Stop, then
// asserts every snapshot PID is gone after Stop returns + a small grace
// window for init to reap.
func TestStopKillsEntireProcessTree(t *testing.T) {
	bin := clickhousePath(t)
	dir := t.TempDir()
	cfg := DefaultConfig(filepath.Join(dir, "ch"))
	cfg.ClickHouseBinary = bin

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	srv, err := Start(ctx, cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	leaderPID := srv.PID()
	if leaderPID <= 0 {
		t.Fatalf("leader PID not set after Start")
	}

	// Wait briefly for any internal worker to fork. clickhouse-server
	// spawns its worker within milliseconds of accepting connections,
	// so 250ms past waitReady is plenty.
	time.Sleep(250 * time.Millisecond)

	pidsBefore := pidsInGroup(t, leaderPID)
	if len(pidsBefore) == 0 {
		t.Fatalf("expected at least the leader (%d) in its process group; got none", leaderPID)
	}
	t.Logf("clickhouse process group before Stop: leader=%d members=%v", leaderPID, pidsBefore)

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// init takes a moment to reap orphans after the parent's Wait
	// fires. 500ms is overkill but flaky-resistant.
	deadline := time.Now().Add(2 * time.Second)
	var stillAlive []int
	for {
		stillAlive = stillAlive[:0]
		for _, p := range pidsBefore {
			if processAlive(p) {
				stillAlive = append(stillAlive, p)
			}
		}
		if len(stillAlive) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(stillAlive) > 0 {
		t.Fatalf("processes survived Stop(): %v (group leader %d)", stillAlive, leaderPID)
	}
}

// pidsInGroup returns every PID currently belonging to the process
// group led by leaderPID. Uses ps(1) so the answer reflects the
// kernel's view rather than what Go knows about cmd's children.
func pidsInGroup(t *testing.T, leaderPID int) []int {
	t.Helper()
	out, err := exec.Command("ps", "-ax", "-o", "pid=,pgid=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	var pids []int
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		var pid, pgid int
		if _, err := fmt.Sscanf(scanner.Text(), "%d %d", &pid, &pgid); err == nil {
			if pgid == leaderPID {
				pids = append(pids, pid)
			}
		}
	}
	return pids
}

// processAlive reports whether pid currently identifies a live process.
// syscall.Kill(pid, 0) returns ESRCH when the process is gone, nil
// while it's still around (including zombies — those are reaped by
// init within milliseconds for our PPID=1 orphans).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

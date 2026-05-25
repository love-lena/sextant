package natsboot

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
// orphan-subprocess class of bugs. A pre-fix Stop() only signaled the
// single leader PID; the spec calls for "SIGINT the whole tree" but the
// implementation did not.
//
// The test snapshots every PID in the leader's group before Stop, then
// asserts every snapshot PID is gone after Stop returns + a small grace
// window for init to reap.
func TestStopKillsEntireProcessTree(t *testing.T) {
	bin := natsServerPath(t)
	dir := t.TempDir()
	cfg := DefaultConfig(filepath.Join(dir, "nats"))
	cfg.NATSBinary = bin

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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

	// Brief settle window — even if nats-server doesn't fork helpers
	// today, the test stays honest if a future bump introduces one.
	time.Sleep(250 * time.Millisecond)

	pidsBefore := pidsInGroup(t, leaderPID)
	if len(pidsBefore) == 0 {
		t.Fatalf("expected at least the leader (%d) in its process group; got none", leaderPID)
	}
	t.Logf("nats-server process group before Stop: leader=%d members=%v", leaderPID, pidsBefore)

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

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
// while it's still around. Zombies satisfy "alive" briefly but get
// reaped by init within milliseconds for PPID=1 orphans, well inside
// the test's grace window.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

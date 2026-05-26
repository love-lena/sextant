package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// buildSextantd compiles the sextantd binary into a fresh dir alongside
// sextant-shipper (the daemon's resolveShipperBinary picks up the sibling
// on startup). Used by the double-start tests that need to spawn a
// second daemon binary independent of the harness.
func buildSextantd(t *testing.T) string {
	t.Helper()
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "sextantd")
	build := exec.Command("go", "build", "-o", binPath, "github.com/love-lena/sextant/cmd/sextantd") //nolint:gosec // test-controlled args
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build sextantd: %v", err)
	}
	shipperBin := filepath.Join(binDir, "sextant-shipper")
	buildShipper := exec.Command("go", "build", "-o", shipperBin, "github.com/love-lena/sextant/cmd/sextant-shipper") //nolint:gosec // test-controlled args
	buildShipper.Stderr = os.Stderr
	if err := buildShipper.Run(); err != nil {
		t.Fatalf("go build sextant-shipper: %v", err)
	}
	return binPath
}

// TestDoubleStart_ExitsZero_WhenAlreadyRunning is the benign-duplicate
// acceptance check: a second sextantd start while one is already running
// must exit 0 with an "already running" message on stderr (not crash on
// port-bind collision).
func TestDoubleStart_ExitsZero_WhenAlreadyRunning(t *testing.T) {
	h := startDaemonHarness(t)
	cfg := h.cfg

	binPath := buildSextantd(t)
	cfgPath := filepath.Join(cfg.Paths.ConfigDir, "sextantd.toml")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "--config", cfgPath) //nolint:gosec // test-controlled args
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = &strings.Builder{}
	err := cmd.Run()
	if err != nil {
		t.Fatalf("second sextantd exit = %v, want nil (exit 0)\nstderr:\n%s\n--- first daemon log ---\n%s",
			err, stderr.String(), h.tail(t))
	}
	if !strings.Contains(stderr.String(), "already running") {
		t.Errorf("second sextantd stderr missing 'already running': %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--restart") {
		t.Errorf("second sextantd stderr missing '--restart' hint: %q", stderr.String())
	}

	// First daemon must still be alive and runtime.json must still point
	// at it.
	rt, err := sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo post-doubled-start: %v", err)
	}
	if rt.PID != h.cmd.Process.Pid {
		t.Errorf("runtime.json PID = %d, want %d (original daemon)", rt.PID, h.cmd.Process.Pid)
	}
}

// TestDoubleStart_StaleRuntimeJSON_StartsNormally covers the orphan
// runtime.json case: file present but the listed PID is dead. The daemon
// must remove the stale file and proceed with normal startup.
func TestDoubleStart_StaleRuntimeJSON_StartsNormally(t *testing.T) {
	requireBins(t)

	configDir, _ := runInitForTest(t)
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Tighten backoff so any startup retry is fast.
	cfg.Daemon.RestartBackoffInitial = sextantd.Duration(100 * time.Millisecond)
	cfg.Daemon.RestartBackoffMax = sextantd.Duration(1 * time.Second)
	cfg.MCP.HTTPPort = 0
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Plant a stale runtime.json — pick a PID that's definitely dead by
	// spawning `true` and reaping it. Wait briefly so the kernel reclaims
	// the slot (PID reuse is unlikely within seconds on Linux/macOS).
	deadCmd := exec.Command("true")
	if err := deadCmd.Run(); err != nil {
		t.Fatalf("spawn dummy: %v", err)
	}
	deadPID := deadCmd.Process.Pid
	if isProcessAlive(deadPID) {
		t.Skipf("PID %d unexpectedly still alive; cannot run stale-runtime test", deadPID)
	}

	stale := sextantd.RuntimeInfo{
		PID:           deadPID,
		StartedAt:     time.Now().UTC().Add(-1 * time.Hour),
		NATSAddr:      "127.0.0.1:1",
		ClickHouseTCP: "127.0.0.1:2",
		ControlSocket: cfg.Daemon.ControlSocket,
		Version:       "stale",
	}
	if err := sextantd.WriteRuntimeInfo(cfg.Paths.RuntimeFile, stale); err != nil {
		t.Fatalf("WriteRuntimeInfo stale: %v", err)
	}

	// Now bring the daemon up via the harness; it should clean up the
	// stale file and start normally.
	h := startDaemonHarnessWithCfgPath(t, cfgPath)

	rt, err := sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo after start: %v", err)
	}
	if rt.PID == deadPID {
		t.Errorf("runtime.json still points at dead pid %d after start", deadPID)
	}
	if rt.PID != h.cmd.Process.Pid {
		t.Errorf("runtime.json PID = %d, want %d (new daemon)", rt.PID, h.cmd.Process.Pid)
	}
}

// TestRestart_GracefullyReplacesRunning starts a daemon, runs a second
// instance with --restart, and asserts the first is terminated and the
// second now owns runtime.json.
func TestRestart_GracefullyReplacesRunning(t *testing.T) {
	h := startDaemonHarness(t)
	cfg := h.cfg
	originalPID := h.cmd.Process.Pid

	binPath := buildSextantd(t)
	cfgPath := filepath.Join(cfg.Paths.ConfigDir, "sextantd.toml")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	logFile, err := os.CreateTemp(t.TempDir(), "sextantd2.log")
	if err != nil {
		t.Fatalf("temp log: %v", err)
	}
	defer logFile.Close() //nolint:errcheck // best-effort

	cmd := exec.CommandContext(ctx, binPath, "--config", cfgPath, "--restart") //nolint:gosec // test-controlled args
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), "SEXTANT_TEST_RUN_LABEL="+testRunLabel())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start second daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGINT)
		done := make(chan struct{})
		go func() {
			_, _ = cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})

	// Wait for the original daemon process to exit. The harness Cleanup
	// would otherwise hang trying to signal a dead PID. Capture the wait
	// here so the cleanup is a no-op.
	originalExit := make(chan error, 1)
	go func() { originalExit <- h.cmd.Wait() }()
	select {
	case <-originalExit:
	case <-time.After(60 * time.Second):
		t.Fatalf("original daemon (pid=%d) did not exit after --restart", originalPID)
	}

	// Wait for runtime.json to point at the new daemon (it will write a
	// fresh file once it boots).
	deadline := time.Now().Add(90 * time.Second)
	var rt sextantd.RuntimeInfo
	for time.Now().Before(deadline) {
		rt, err = sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
		if err == nil && rt.PID == cmd.Process.Pid {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if rt.PID != cmd.Process.Pid {
		raw, _ := os.ReadFile(logFile.Name())
		t.Fatalf("runtime.json PID = %d, want %d (new daemon)\n--- new daemon log ---\n%s", rt.PID, cmd.Process.Pid, raw)
	}
	if rt.PID == originalPID {
		t.Errorf("runtime.json still points at the original daemon pid %d", originalPID)
	}
}

// TestRestart_TimesOut_WhenOldDaemonHangs verifies the safety bound on
// --restart: if the previously listed daemon ignores SIGTERM, sextantd
// exits 1 within the configured graceful-wait window rather than blocking
// forever. We stub the running daemon with `sh -c 'trap "" TERM; ...'`
// — a real process that traps SIGTERM but accepts SIGKILL at cleanup.
func TestRestart_TimesOut_WhenOldDaemonHangs(t *testing.T) {
	requireBins(t)

	configDir, _ := runInitForTest(t)
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Spawn the SIGTERM-immune fake daemon.
	fake := exec.Command("sh", "-c", `trap "" TERM; while :; do sleep 30; done`) //nolint:gosec // controlled command
	if err := fake.Start(); err != nil {
		t.Fatalf("spawn fake daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = fake.Process.Kill()
		_, _ = fake.Process.Wait()
	})

	// Plant a runtime.json pointing at the fake's PID.
	stale := sextantd.RuntimeInfo{
		PID:           fake.Process.Pid,
		StartedAt:     time.Now().UTC().Add(-10 * time.Minute),
		NATSAddr:      "127.0.0.1:1",
		ClickHouseTCP: "127.0.0.1:2",
		ControlSocket: cfg.Daemon.ControlSocket,
		Version:       "fake",
	}
	if err := sextantd.WriteRuntimeInfo(cfg.Paths.RuntimeFile, stale); err != nil {
		t.Fatalf("WriteRuntimeInfo: %v", err)
	}

	binPath := buildSextantd(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binPath, "--config", cfgPath, "--restart") //nolint:gosec // test-controlled args
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = &strings.Builder{}
	// Override the graceful-wait timeout to a short value so the test
	// finishes in a few seconds rather than waiting the production 30s.
	cmd.Env = append(os.Environ(), "SEXTANTD_RESTART_WAIT=2s")

	start := time.Now()
	err = cmd.Run()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("sextantd --restart against hung daemon exited 0; want non-zero")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1; stderr=%q", exitErr.ExitCode(), stderr.String())
	}
	if elapsed > 15*time.Second {
		t.Errorf("--restart wait took %s, want <= ~15s (timeout 2s + slack)", elapsed)
	}
	if !strings.Contains(stderr.String(), "did not shut down") &&
		!strings.Contains(stderr.String(), "timed out") {
		t.Errorf("stderr missing timeout message: %q", stderr.String())
	}
}

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextantd"
)

// TestLogFile_CreatedOnStartup brings the daemon up against a fresh init'd
// dir and asserts <data_dir>/sextantd.log exists with mode 0600 once the
// control-socket greeting lands. This is the canonical path operators
// and `sextant doctor` expect to point at when investigating a daemon
// that exited.
func TestLogFile_CreatedOnStartup(t *testing.T) {
	h := startDaemonHarness(t)
	cfg := h.cfg

	want := filepath.Join(cfg.Paths.DataDir, "sextantd.log")
	st, err := os.Stat(want)
	if err != nil {
		t.Fatalf("stat %s: %v", want, err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Errorf("log file mode = %o, want 0600", got)
	}
}

// TestLogFile_AppendsOnRestart starts the daemon, captures the log
// contents, shuts down cleanly, then starts a second daemon against the
// same config + data dir and asserts the second startup *appended* to
// the existing file (i.e. the first daemon's log lines survive across
// the restart). The append-mode contract is what makes this file
// useful for post-mortem debugging.
func TestLogFile_AppendsOnRestart(t *testing.T) {
	requireBins(t)

	configDir, _ := runInitForTest(t)
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Daemon.RestartBackoffInitial = sextantd.Duration(100 * time.Millisecond)
	cfg.Daemon.RestartBackoffMax = sextantd.Duration(1 * time.Second)
	cfg.MCP.HTTPPort = 0
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	logPath := filepath.Join(cfg.Paths.DataDir, "sextantd.log")

	// First daemon run.
	h1 := startDaemonHarnessWithCfgPath(t, cfgPath)
	first, err := os.ReadFile(logPath) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read log after first start: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("log file empty after first start")
	}
	// Pull a stable substring out of the first run so we can assert it
	// survives the second start's append.
	firstStartedMarker := "sextantd: starting"
	if !strings.Contains(string(first), firstStartedMarker) {
		t.Fatalf("first log missing %q marker; got:\n%s", firstStartedMarker, first)
	}

	// Shut the first daemon down cleanly.
	if err := h1.cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("SIGINT first: %v", err)
	}
	exitCh := make(chan error, 1)
	go func() { exitCh <- h1.cmd.Wait() }()
	select {
	case <-exitCh:
	case <-time.After(cfg.Daemon.ShutdownTimeout.AsDuration() + 15*time.Second):
		_ = h1.cmd.Process.Kill()
		t.Fatalf("first daemon did not exit\n--- daemon log ---\n%s", h1.tail(t))
	}

	// Snapshot the exact bytes the first run wrote so we can assert the
	// second run appended (rather than truncated).
	afterShutdown, err := os.ReadFile(logPath) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read log after first shutdown: %v", err)
	}

	// Second daemon run.
	h2 := startDaemonHarnessWithCfgPath(t, cfgPath)
	defer func() {
		_ = h2.cmd.Process.Signal(syscall.SIGINT)
		_, _ = h2.cmd.Process.Wait()
	}()

	second, err := os.ReadFile(logPath) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read log after second start: %v", err)
	}
	if len(second) <= len(afterShutdown) {
		t.Fatalf("second start did not append: before=%d bytes, after=%d bytes", len(afterShutdown), len(second))
	}
	if !strings.HasPrefix(string(second), string(afterShutdown)) {
		t.Fatalf("second start did not preserve first-run bytes (truncate, not append?)\nfirst:\n%s\nsecond:\n%s",
			afterShutdown, second)
	}
}

// TestLogFile_TeesToStderr asserts that the daemon's startup log lines
// land in BOTH the log file AND its stderr stream — foreground operators
// keep terminal feedback while the file always captures.
func TestLogFile_TeesToStderr(t *testing.T) {
	requireBins(t)

	configDir, _ := runInitForTest(t)
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	cfg.Daemon.RestartBackoffInitial = sextantd.Duration(100 * time.Millisecond)
	cfg.Daemon.RestartBackoffMax = sextantd.Duration(1 * time.Second)
	cfg.MCP.HTTPPort = 0
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// Build the binary the same way startDaemonHarnessWithCfgPath does
	// so we can route stderr to our own buffer instead of the harness's
	// shared tail log (which mixes stdout + stderr).
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "sextantd")
	if err := exec.Command("go", "build", "-o", binPath, "github.com/love-lena/sextant/cmd/sextantd").Run(); err != nil { //nolint:gosec // test paths
		t.Fatalf("go build sextantd: %v", err)
	}
	shipperBin := filepath.Join(binDir, "sextant-shipper")
	if err := exec.Command("go", "build", "-o", shipperBin, "github.com/love-lena/sextant/cmd/sextant-shipper").Run(); err != nil { //nolint:gosec // test paths
		t.Fatalf("go build sextant-shipper: %v", err)
	}

	stderrPath := filepath.Join(binDir, "stderr.log")
	stderrFile, err := os.Create(stderrPath) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("create stderr: %v", err)
	}
	t.Cleanup(func() { _ = stderrFile.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, binPath, "--config", cfgPath) //nolint:gosec // test paths
	cmd.Stderr = stderrFile
	cmd.Env = append(os.Environ(), "SEXTANT_TEST_RUN_LABEL="+testRunLabel())
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGINT)
		done := make(chan struct{})
		go func() { _, _ = cmd.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})

	// Wait for the daemon to greet via the control socket — same gating
	// mechanism the shared harness uses. At that point the "starting"
	// log line has been written to both sinks.
	if _, err := waitForGreeting(ctx, cfg.Daemon.ControlSocket, 75*time.Second); err != nil {
		t.Fatalf("greeting: %v", err)
	}

	// Force a stderr flush before reading.
	if err := stderrFile.Sync(); err != nil {
		t.Fatalf("sync stderr: %v", err)
	}

	stderrBytes, err := os.ReadFile(stderrPath) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	const marker = "sextantd: starting"
	if !strings.Contains(string(stderrBytes), marker) {
		t.Fatalf("stderr missing %q; the daemon log is not tee'd to stderr.\nstderr:\n%s", marker, stderrBytes)
	}

	logBytes, err := os.ReadFile(filepath.Join(cfg.Paths.DataDir, "sextantd.log")) //nolint:gosec // test path
	if err != nil {
		t.Fatalf("read sextantd.log: %v", err)
	}
	if !strings.Contains(string(logBytes), marker) {
		t.Fatalf("sextantd.log missing %q; the daemon log was not captured.\nlog:\n%s", marker, logBytes)
	}
}

// TestRuntimeInfo_HasLogFile asserts the daemon publishes the log path
// via runtime.json so clients (doctor, sidecar, ops tools) can discover
// it without re-deriving the default path.
func TestRuntimeInfo_HasLogFile(t *testing.T) {
	h := startDaemonHarness(t)
	cfg := h.cfg

	rt, err := sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	want := filepath.Join(cfg.Paths.DataDir, "sextantd.log")
	if rt.LogFile != want {
		t.Errorf("runtime.json LogFile = %q, want %q", rt.LogFile, want)
	}
	// And the path must actually point at a file the daemon wrote.
	if _, err := os.Stat(rt.LogFile); err != nil {
		t.Errorf("stat advertised log file %s: %v", rt.LogFile, err)
	}
}

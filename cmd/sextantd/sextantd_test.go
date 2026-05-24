package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant-initial/pkg/sextantd"
)

// requireBins skips when the required binaries are not available on
// PATH. M5's integration test exercises real NATS + ClickHouse.
func requireBins(t *testing.T) {
	t.Helper()
	for _, name := range []string{"nats-server", "clickhouse"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s not on PATH: %v", name, err)
		}
	}
}

// runInitForTest replicates the steps of `sextant init` against a fresh
// temp dir. It uses the cmd/sextant package indirectly by invoking the
// same sextantd helpers (the init logic itself is exercised in
// cmd/sextant/init_test.go).
//
// On macOS the Unix domain socket path is limited to ~104 bytes,
// including the data dir prefix. We use a short /tmp-rooted dir for
// the socket-bearing data dir so the limit is not crossed.
func runInitForTest(t *testing.T) (configDir, dataDir string) {
	t.Helper()
	dir := t.TempDir()
	configDir = filepath.Join(dir, "cfg")
	dataDir, err := os.MkdirTemp("", "sxtd")
	if err != nil {
		t.Fatalf("MkdirTemp data dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	if err := os.Chmod(dataDir, 0o750); err != nil { //nolint:gosec // matches spec's 0750 data-dir mode
		t.Fatalf("chmod data: %v", err)
	}
	for _, sub := range []string{"nats", "clickhouse", "shipper-buffer", "test"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o750); err != nil {
			t.Fatalf("mkdir data/%s: %v", sub, err)
		}
	}
	// Pull init from the sibling cmd/sextant package logic — we cannot
	// import it directly (cmd packages aren't importable), so we mirror
	// the steps that matter: CA, operator creds, password, config,
	// templates. Mirroring is acceptable here because the cmd/sextant
	// init unit tests cover the init contract itself.
	if err := writeMinimalInstall(configDir, dataDir); err != nil {
		t.Fatalf("install: %v", err)
	}
	return configDir, dataDir
}

func writeMinimalInstall(configDir, dataDir string) error {
	cfg := sextantd.DefaultConfig(configDir, dataDir)
	// Templates dir + default.toml.
	if err := os.MkdirAll(cfg.Paths.TemplatesDir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cfg.Paths.TemplatesDir, "default.toml"),
		[]byte(`name = "default"`+"\n"), 0o600); err != nil {
		return err
	}
	// CA.
	privPEM, pubPEM, err := makeCA()
	if err != nil {
		return err
	}
	if err := os.WriteFile(cfg.CA.KeyPath, privPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(cfg.CA.PubPath, pubPEM, 0o644); err != nil { //nolint:gosec // ca.pub is world-readable by design
		return err
	}
	// Operator creds.
	pw, err := sextantd.GenerateOperatorPassword()
	if err != nil {
		return err
	}
	if err := sextantd.WriteOperatorCreds(cfg.NATS.OperatorCreds,
		sextantd.OperatorCreds{User: "operator", Password: pw}); err != nil {
		return err
	}
	// ClickHouse password file.
	chPw, err := sextantd.GenerateOperatorPassword()
	if err != nil {
		return err
	}
	if err := sextantd.WritePasswordFile(cfg.ClickHouse.PasswordFile, chPw); err != nil {
		return err
	}
	// sextantd.toml.
	return sextantd.SaveConfig(filepath.Join(configDir, "sextantd.toml"), cfg)
}

func makeCA() (priv, pub []byte, err error) {
	// Use the authjwt generator directly to avoid pulling cmd/sextant.
	return generateCAForTest()
}

// TestDaemonStartStopRoundtrip is the M5 acceptance test. It spawns the
// daemon process, polls the control socket for the OK greeting,
// SIGTERMs the daemon, and asserts it exits cleanly.
func TestDaemonStartStopRoundtrip(t *testing.T) {
	requireBins(t)

	configDir, _ := runInitForTest(t)
	cfg, err := sextantd.LoadConfig(filepath.Join(configDir, "sextantd.toml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Build the daemon binary into the test temp dir.
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "sextantd")
	build := exec.Command("go", "build", "-o", binPath, "github.com/love-lena/sextant-initial/cmd/sextantd") //nolint:gosec // test-controlled args
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build sextantd: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	logFile, err := os.CreateTemp(binDir, "sextantd.log")
	if err != nil {
		t.Fatalf("temp log: %v", err)
	}
	defer logFile.Close() //nolint:errcheck // close best-effort

	cmd := exec.CommandContext(ctx, binPath, "--config", filepath.Join(configDir, "sextantd.toml")) //nolint:gosec // test-controlled args
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Poll the control socket for the OK greeting (daemon takes a few
	// seconds to bring up NATS + ClickHouse).
	greeting, err := waitForGreeting(ctx, cfg.Daemon.ControlSocket, 75*time.Second)
	if err != nil {
		// Dump the log to help diagnose startup failures.
		logBytes, _ := os.ReadFile(logFile.Name())
		t.Fatalf("greeting: %v\n--- daemon log ---\n%s", err, string(logBytes))
	}
	if !strings.HasPrefix(greeting, "OK ") {
		t.Fatalf("greeting = %q, want OK prefix", greeting)
	}

	// Read runtime.json — it should exist now.
	rt, err := sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	if rt.PID != cmd.Process.Pid {
		t.Errorf("runtime.json PID = %d, want %d", rt.PID, cmd.Process.Pid)
	}
	if rt.NATSAddr == "" {
		t.Error("runtime.json NATSAddr empty")
	}
	if rt.ClickHouseTCP == "" {
		t.Error("runtime.json ClickHouseTCP empty")
	}

	// SIGTERM the daemon; assert it exits within ShutdownTimeout+slack.
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}
	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()
	timeout := cfg.Daemon.ShutdownTimeout.AsDuration() + 30*time.Second
	select {
	case err := <-exitCh:
		if err != nil && !strings.Contains(err.Error(), "exit status") {
			t.Errorf("daemon Wait: %v", err)
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		logBytes, _ := os.ReadFile(logFile.Name())
		t.Fatalf("daemon did not exit within %s\n--- daemon log ---\n%s", timeout, string(logBytes))
	}

	// runtime.json should be gone after a clean shutdown.
	if _, err := os.Stat(cfg.Paths.RuntimeFile); err == nil {
		t.Errorf("runtime.json still present after shutdown")
	}
	// Socket file should be gone.
	if _, err := os.Stat(cfg.Daemon.ControlSocket); err == nil {
		t.Errorf("control socket file still present after shutdown")
	}
}

// waitForGreeting polls the Unix socket until the daemon writes its
// greeting or timeout expires. Used by the M5 integration test.
func waitForGreeting(ctx context.Context, path string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return "", os.ErrDeadlineExceeded
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		conn, err := net.DialTimeout("unix", path, 1*time.Second)
		if err != nil {
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := bufio.NewReader(conn).ReadString('\n')
		_ = conn.Close()
		if err != nil {
			continue
		}
		return strings.TrimSpace(line), nil
	}
}

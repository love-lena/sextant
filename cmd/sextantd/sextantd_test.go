package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

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

// daemonHarness is the test fixture shared by the daemon integration
// tests. It builds the sextantd binary, runs `sextant init` in a temp
// home, starts the daemon, waits for the control-socket greeting, and
// surfaces the cfg + cmd handle.
type daemonHarness struct {
	cfg     sextantd.Config
	cmd     *exec.Cmd
	logFile *os.File
	ctx     context.Context
	cancel  context.CancelFunc
}

func (h *daemonHarness) tail(t *testing.T) string {
	t.Helper()
	raw, _ := os.ReadFile(h.logFile.Name())
	return string(raw)
}

// startDaemonHarness brings the daemon up against a fresh init'd dir.
// The harness cleans up on test exit. The daemon comes up with the
// faster M5-test backoff (100ms initial / 1s cap) so restart-on-failure
// tests don't wait on the production default 1s/5min.
func startDaemonHarness(t *testing.T) *daemonHarness {
	t.Helper()
	requireBins(t)

	configDir, _ := runInitForTest(t)
	cfgPath := filepath.Join(configDir, "sextantd.toml")
	cfg, err := sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Tighten the backoff knobs so restart tests don't wait on prod
	// defaults; this writes back through the same SaveConfig path so
	// the daemon picks it up.
	cfg.Daemon.RestartBackoffInitial = sextantd.Duration(100 * time.Millisecond)
	cfg.Daemon.RestartBackoffMax = sextantd.Duration(1 * time.Second)
	if err := sextantd.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig (tightened): %v", err)
	}
	cfg, err = sextantd.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig (post-tighten): %v", err)
	}

	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "sextantd")
	build := exec.Command("go", "build", "-o", binPath, "github.com/love-lena/sextant-initial/cmd/sextantd") //nolint:gosec // test-controlled args
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build sextantd: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	logFile, err := os.CreateTemp(binDir, "sextantd.log")
	if err != nil {
		cancel()
		t.Fatalf("temp log: %v", err)
	}

	cmd := exec.CommandContext(ctx, binPath, "--config", cfgPath) //nolint:gosec // test-controlled args
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatalf("start daemon: %v", err)
	}

	h := &daemonHarness{cfg: cfg, cmd: cmd, logFile: logFile, ctx: ctx, cancel: cancel}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = logFile.Close()
		cancel()
	})

	greeting, err := waitForGreeting(ctx, cfg.Daemon.ControlSocket, 75*time.Second)
	if err != nil {
		t.Fatalf("greeting: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if !strings.HasPrefix(greeting, "OK ") {
		t.Fatalf("greeting = %q, want OK prefix", greeting)
	}
	return h
}

// TestDaemonStartStopRoundtrip is the M5 acceptance test. It spawns the
// daemon process, polls the control socket for the OK greeting,
// SIGTERMs the daemon, and asserts it exits cleanly.
func TestDaemonStartStopRoundtrip(t *testing.T) {
	h := startDaemonHarness(t)
	cfg := h.cfg

	// Read runtime.json — it should exist now.
	rt, err := sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	if rt.PID != h.cmd.Process.Pid {
		t.Errorf("runtime.json PID = %d, want %d", rt.PID, h.cmd.Process.Pid)
	}
	if rt.NATSAddr == "" {
		t.Error("runtime.json NATSAddr empty")
	}
	if rt.ClickHouseTCP == "" {
		t.Error("runtime.json ClickHouseTCP empty")
	}
	if rt.NATSPID == 0 {
		t.Error("runtime.json NATSPID empty")
	}
	if rt.ClickHousePID == 0 {
		t.Error("runtime.json ClickHousePID empty")
	}

	// SIGTERM the daemon; assert it exits within ShutdownTimeout+slack.
	if err := h.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}
	exitCh := make(chan error, 1)
	go func() { exitCh <- h.cmd.Wait() }()
	timeout := cfg.Daemon.ShutdownTimeout.AsDuration() + 30*time.Second
	select {
	case err := <-exitCh:
		if err != nil && !strings.Contains(err.Error(), "exit status") {
			t.Errorf("daemon Wait: %v", err)
		}
	case <-time.After(timeout):
		_ = h.cmd.Process.Kill()
		t.Fatalf("daemon did not exit within %s\n--- daemon log ---\n%s", timeout, h.tail(t))
	}

	if _, err := os.Stat(cfg.Paths.RuntimeFile); err == nil {
		t.Errorf("runtime.json still present after shutdown")
	}
	if _, err := os.Stat(cfg.Daemon.ControlSocket); err == nil {
		t.Errorf("control socket file still present after shutdown")
	}
}

// TestDaemonRestartsNATSAfterKill is the restart-on-failure acceptance
// test. We bring the daemon up, set up an operator NATS subscription
// using the same reconnect knobs pkg/client uses, SIGKILL the NATS
// subprocess externally, and assert:
//
//   - the supervisor restarts NATS within the configured backoff window
//   - runtime.json reflects the new PID
//   - the operator's nats.Conn auto-reconnects across the restart (the
//     same connection + subscription handles keep working — no manual
//     reconnect, no resubscribe)
//   - a message published *after* the restart is delivered through the
//     same subscription that was set up *before* the kill
//   - the daemon does not exit on its own
//
// The subscription survival is the load-bearing check: the spec at
// specs/components/sextantd.md §"Supervision details" claims operator
// NATS clients reconnect cleanly across a restart, and this test is
// what proves that claim.
func TestDaemonRestartsNATSAfterKill(t *testing.T) {
	h := startDaemonHarness(t)
	cfg := h.cfg

	rt, err := sextantd.ReadRuntimeInfo(cfg.Paths.RuntimeFile)
	if err != nil {
		t.Fatalf("ReadRuntimeInfo: %v", err)
	}
	originalNATSPID := rt.NATSPID
	if originalNATSPID == 0 {
		t.Fatal("runtime.json missing NATSPID — daemon did not record subprocess pid")
	}
	originalNATSAddr := rt.NATSAddr

	// Connect as the operator with the same reconnect knobs pkg/client
	// uses (specs/components/client-libraries.md §"Shared concerns").
	// We use a raw nats.Connect rather than pkg/client because the
	// claim under test lives at the nats.Conn layer; pkg/client
	// inherits this behavior without adding to it.
	creds, err := sextantd.ReadOperatorCreds(cfg.NATS.OperatorCreds)
	if err != nil {
		t.Fatalf("ReadOperatorCreds: %v", err)
	}
	reconnectCh := make(chan string, 4)
	disconnectCh := make(chan struct{}, 4)
	nc, err := nats.Connect(
		"nats://"+originalNATSAddr,
		nats.UserInfo(creds.User, creds.Password),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(100*time.Millisecond),
		nats.ReconnectJitter(50*time.Millisecond, 100*time.Millisecond),
		nats.Timeout(2*time.Second),
		nats.ReconnectHandler(func(c *nats.Conn) {
			select {
			case reconnectCh <- c.ConnectedUrl():
			default:
			}
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, _ error) {
			select {
			case disconnectCh <- struct{}{}:
			default:
			}
		}),
	)
	if err != nil {
		t.Fatalf("operator nats connect: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	defer nc.Close()

	const subject = "sextant.test.restart-roundtrip"
	msgCh := make(chan *nats.Msg, 4)
	sub, err := nc.ChanSubscribe(subject, msgCh)
	if err != nil {
		t.Fatalf("subscribe pre-kill: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := nc.Flush(); err != nil {
		t.Fatalf("flush pre-kill: %v", err)
	}

	// Sanity: pre-kill publish + receive.
	if err := nc.Publish(subject, []byte("pre-kill")); err != nil {
		t.Fatalf("publish pre-kill: %v", err)
	}
	select {
	case m := <-msgCh:
		if string(m.Data) != "pre-kill" {
			t.Fatalf("pre-kill msg = %q, want %q", m.Data, "pre-kill")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pre-kill message not received")
	}

	// Kill the NATS subprocess externally with SIGKILL (no graceful
	// shutdown — simulates a real crash).
	proc, err := os.FindProcess(originalNATSPID)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", originalNATSPID, err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatalf("kill nats pid %d: %v", originalNATSPID, err)
	}
	t.Logf("killed nats pid=%d", originalNATSPID)

	// Wait for the supervisor to restart NATS. With tightened backoff
	// (100ms initial / 1s cap) + nats-server's startup latency, recovery
	// lands well under 15s.
	newPID, newAddr, err := waitForNATSRestart(
		cfg.Paths.RuntimeFile,
		originalNATSPID,
		15*time.Second,
	)
	if err != nil {
		t.Fatalf("waitForNATSRestart: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	t.Logf("nats restarted: old=%d new=%d addr=%s", originalNATSPID, newPID, newAddr)

	if newPID == originalNATSPID {
		t.Errorf("NATSPID did not change after kill (still %d)", newPID)
	}

	// Wait for the operator's nats.Conn to observe the reconnect. With
	// frozen ports the new listener is at originalNATSAddr; nats.go's
	// reconnect loop reconnects to the same URL.
	select {
	case url := <-reconnectCh:
		t.Logf("operator nats.Conn reconnected to %s", url)
	case <-time.After(15 * time.Second):
		t.Fatalf("operator nats.Conn never reported reconnect\n--- daemon log ---\n%s", h.tail(t))
	}

	// We must have observed at least one disconnect along the way.
	select {
	case <-disconnectCh:
	default:
		t.Error("operator nats.Conn did not report any disconnect")
	}

	// The original subscription handle must still be valid. Publish
	// post-restart through the same nats.Conn and assert the message
	// lands on the same msgCh. This is the round-trip the spec claims
	// works.
	pubErr := publishWithRetry(nc, subject, []byte("post-restart"), 5*time.Second)
	if pubErr != nil {
		t.Fatalf("publish post-restart: %v", pubErr)
	}
	select {
	case m := <-msgCh:
		if string(m.Data) != "post-restart" {
			t.Fatalf("post-restart msg = %q, want %q", m.Data, "post-restart")
		}
		t.Logf("round-trip via reconnected subscription: %q", m.Data)
	case <-time.After(5 * time.Second):
		t.Fatalf("post-restart message not received on reconnected subscription\n--- daemon log ---\n%s", h.tail(t))
	}

	if !sub.IsValid() {
		t.Error("subscription handle invalid after reconnect")
	}
	_ = newAddr // newAddr is logged above; the round-trip via nc proves connectivity.

	// Daemon should still be running.
	if h.cmd.ProcessState != nil && h.cmd.ProcessState.Exited() {
		t.Fatalf("daemon exited unexpectedly after nats restart: %v", h.cmd.ProcessState)
	}

	// Clean shutdown should still work.
	if err := h.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("SIGINT: %v", err)
	}
	exitCh := make(chan error, 1)
	go func() { exitCh <- h.cmd.Wait() }()
	select {
	case <-exitCh:
	case <-time.After(cfg.Daemon.ShutdownTimeout.AsDuration() + 30*time.Second):
		_ = h.cmd.Process.Kill()
		t.Fatalf("daemon did not exit after restart-and-SIGINT\n--- daemon log ---\n%s", h.tail(t))
	}
}

// publishWithRetry covers the brief window during which the new
// listener is bound but the operator's nats.Conn has not yet rebound
// its publisher state. nats.go queues publishes during reconnect, but
// in some failure modes the publish call itself can return a transient
// error; we retry with backoff up to the supplied total timeout.
func publishWithRetry(nc *nats.Conn, subject string, data []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := nc.Publish(subject, data); err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if err := nc.FlushTimeout(2 * time.Second); err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("publishWithRetry: timed out")
	}
	return lastErr
}

// waitForNATSRestart polls runtime.json until NATSPID differs from
// excludePID and the listener at the recorded NATSAddr accepts a TCP
// connection. Returns the new pid + addr.
func waitForNATSRestart(runtimePath string, excludePID int, timeout time.Duration) (int, string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return 0, "", fmt.Errorf("timed out after %s waiting for nats restart (pid still %d)", timeout, excludePID)
		}
		rt, err := sextantd.ReadRuntimeInfo(runtimePath)
		if err == nil && rt.NATSPID != 0 && rt.NATSPID != excludePID {
			if err := dialNATS(rt.NATSAddr); err == nil {
				return rt.NATSPID, rt.NATSAddr, nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// dialNATS confirms a TCP listener accepts a connection at addr.
// We use a raw TCP dial rather than nats.Connect because the latter
// would require us to wire credentials and authentication into the
// test fixture for a check that we only need at the transport layer.
func dialNATS(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
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

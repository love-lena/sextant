package natsboot

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// natsServerPath skips the test if nats-server is not available on
// $PATH. Useful in CI environments that don't have it installed.
func natsServerPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}
	return p
}

// TestStartAndStop verifies the basic subprocess lifecycle: nats-server
// boots, accepts a connection, and stops cleanly.
func TestStartAndStop(t *testing.T) {
	bin := natsServerPath(t)
	dir := t.TempDir()
	cfg := DefaultConfig(filepath.Join(dir, "nats"))
	cfg.NATSBinary = bin
	cfg.LogFile = filepath.Join(dir, "nats.log")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	srv, err := Start(ctx, cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })

	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	nc.Close()

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestBootstrapCreatesEverything covers the acceptance criterion for M2:
// every declared stream and KV bucket exists after Bootstrap.
func TestBootstrapCreatesEverything(t *testing.T) {
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

	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()

	if err := Bootstrap(ctx, nc, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if err := VerifyBootstrap(ctx, nc); err != nil {
		t.Fatalf("VerifyBootstrap: %v", err)
	}

	// Idempotency: a second Bootstrap on the same server must succeed.
	if err := Bootstrap(ctx, nc, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("Bootstrap (second call): %v", err)
	}
}

// TestRoundtripOverOperatorListener is the M2 acceptance test: publish on
// a real subject and observe it on a real consumer over the loopback
// listener using operator credentials.
func TestRoundtripOverOperatorListener(t *testing.T) {
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

	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()

	if err := Bootstrap(ctx, nc, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}

	// Publish into agents.<uuid>.lifecycle and consume it via the
	// agent_lifecycle stream. Verifies wildcard subjects, JetStream
	// publish, and ephemeral consumer in one go.
	subject := "agents.aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.lifecycle"
	if _, err := js.Publish(ctx, subject, []byte(`{"hello":"world"}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	stream, err := js.Stream(ctx, "agent_lifecycle")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("CreateOrUpdateConsumer: %v", err)
	}
	msgs, err := consumer.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got := 0
	for m := range msgs.Messages() {
		got++
		if string(m.Data()) != `{"hello":"world"}` {
			t.Fatalf("data = %s", m.Data())
		}
		if m.Subject() != subject {
			t.Fatalf("subject = %s, want %s", m.Subject(), subject)
		}
		if err := m.Ack(); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	if msgs.Error() != nil {
		t.Fatalf("Fetch error: %v", msgs.Error())
	}
	if got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
}

// TestWildcardSubjectsCoverMultiToken pins down the bus-subjects.md rule
// that telemetry.metrics.> must match deeply-nested subjects.
func TestWildcardSubjectsCoverMultiToken(t *testing.T) {
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

	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer nc.Close()

	if err := Bootstrap(ctx, nc, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}

	// Deeply-nested telemetry subject.
	subject := "telemetry.metrics.shipper.host-a.lag_seconds"
	if _, err := js.Publish(ctx, subject, []byte(`{"v":1.23}`)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	stream, err := js.Stream(ctx, "telemetry_metrics")
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("Stream Info: %v", err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("telemetry_metrics msg count = %d, want 1", info.State.Msgs)
	}
}

// TestStartFailsCleanlyWhenBinaryMissing exercises the error path; no
// nats-server should be left running.
func TestStartFailsCleanlyWhenBinaryMissing(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig(filepath.Join(dir, "nats"))
	cfg.NATSBinary = filepath.Join(dir, "no-such-nats-server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := Start(ctx, cfg); err == nil {
		t.Fatal("expected Start to fail when binary missing")
	}
}

// TestConfigValidationRejectsRoutableBind keeps a regression guard on the
// "loopback-only" decision.
func TestConfigValidationRejectsRoutableBind(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.ListenHost = "0.0.0.0"
	if _, err := cfg.validateAndFill(); err == nil {
		t.Fatal("expected validateAndFill to reject 0.0.0.0 bind")
	}
}

// TestConfigGeneratesPasswordWhenAbsent asserts every Start has a real
// auth secret. Defends against accidental no-auth config.
func TestConfigGeneratesPasswordWhenAbsent(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	cfg.OperatorPassword = ""
	out, err := cfg.validateAndFill()
	if err != nil {
		t.Fatalf("validateAndFill: %v", err)
	}
	if out.OperatorPassword == "" {
		t.Fatal("OperatorPassword should be auto-generated")
	}
	if len(out.OperatorPassword) < 20 {
		t.Fatalf("OperatorPassword too short: %d chars", len(out.OperatorPassword))
	}
}

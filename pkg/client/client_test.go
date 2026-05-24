package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/natsboot"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// natsServerPath skips the test if nats-server is not on PATH; matches
// the helper in pkg/natsboot/natsboot_test.go.
func natsServerPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}
	return p
}

// bootedServer brings up a NATS server with the sextant layout applied
// and registers a t.Cleanup to stop it. Returns the running server.
//
// The server's subprocess is bound to context.Background() so it
// survives until t.Cleanup runs — natsboot uses exec.CommandContext,
// which SIGKILLs the subprocess when its parent ctx is canceled.
func bootedServer(t *testing.T) *natsboot.Server {
	t.Helper()
	bin := natsServerPath(t)
	dir := t.TempDir()
	cfg := natsboot.DefaultConfig(filepath.Join(dir, "nats"))
	cfg.NATSBinary = bin

	startCtx, startCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer startCancel()
	// Use a separate, longer-lived ctx for Start so the subprocess is
	// not torn down when this helper returns.
	srv, err := natsboot.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("natsboot.Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = srv.Stop(stopCtx)
	})

	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("srv.Connect: %v", err)
	}
	defer nc.Close()
	if err := natsboot.Bootstrap(startCtx, nc, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("natsboot.Bootstrap: %v", err)
	}
	return srv
}

// clientFromServer returns a connected *client.Client for the booted
// NATS server. ctx is the lifetime of the client.
func clientFromServer(ctx context.Context, t *testing.T, srv *natsboot.Server) *client.Client {
	t.Helper()
	cfg := client.Config{
		NATS:     client.NATSConfig{URL: srv.PublicURL()},
		Operator: client.OperatorConfig{User: srv.OperatorUser(), Password: srv.OperatorPassword()},
	}
	cli, err := client.ConnectWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("client.ConnectWithConfig: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// TestSubscribeRoundTripsEnvelopeWithStreamSeq is the M4 acceptance test:
// publish an envelope on agents.<uuid>.frames, observe it on the
// subscription channel with StreamSeq populated, and confirm the
// envelope round-tripped intact.
func TestSubscribeRoundTripsEnvelopeWithStreamSeq(t *testing.T) {
	srv := bootedServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cli := clientFromServer(ctx, t, srv)

	subject := "agents.aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.frames"
	subCtx, subCancel := context.WithCancel(ctx)
	defer subCancel()
	ch, err := cli.Subscribe(subCtx, subject, client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Build a real envelope using sextantproto and publish via the
	// raw JS client (no client.Publish in M4 — by design).
	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from, map[string]any{"hello": "world"})
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal envelope: %v", err)
	}
	if _, err := cli.JetStream().Publish(ctx, subject, raw); err != nil {
		t.Fatalf("JS Publish: %v", err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("subscription channel closed before delivery")
		}
		if msg.Subject != subject {
			t.Fatalf("Subject = %q, want %q", msg.Subject, subject)
		}
		if msg.StreamSeq == 0 {
			t.Fatal("StreamSeq must be populated (codex HIGH item)")
		}
		if msg.ConsumerSeq == 0 {
			t.Fatal("ConsumerSeq must be populated")
		}
		if msg.Envelope.ID != env.ID {
			t.Fatalf("Envelope.ID = %s, want %s", msg.Envelope.ID, env.ID)
		}
		if msg.Envelope.Kind != sextantproto.KindAgentFrame {
			t.Fatalf("Envelope.Kind = %s, want agent_frame", msg.Envelope.Kind)
		}
		var payload map[string]string
		if err := json.Unmarshal(msg.Envelope.Payload, &payload); err != nil {
			t.Fatalf("payload unmarshal: %v", err)
		}
		if payload["hello"] != "world" {
			t.Fatalf("payload = %v", payload)
		}
		if err := msg.Ack(); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TestSubscribeFromSeqResumes verifies that SubscribeFromSeq starts at
// the requested stream sequence, not from the beginning or newest. This
// is the load-bearing behavior for client reconnection.
func TestSubscribeFromSeqResumes(t *testing.T) {
	srv := bootedServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cli := clientFromServer(ctx, t, srv)

	subject := "agents.bbbbbbbb-cccc-dddd-eeee-ffffffffffff.frames"
	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"}

	publish := func(i int) sextantproto.Envelope {
		env, perr := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from, map[string]int{"i": i})
		if perr != nil {
			t.Fatalf("NewEnvelopeWith: %v", perr)
		}
		raw, perr := json.Marshal(env)
		if perr != nil {
			t.Fatalf("Marshal envelope: %v", perr)
		}
		if _, perr := cli.JetStream().Publish(ctx, subject, raw); perr != nil {
			t.Fatalf("JS Publish: %v", perr)
		}
		return env
	}

	// Pre-publish 3 envelopes so the stream has real history. Capture
	// the second one's ID so we can pin the resume point.
	publish(0)
	second := publish(1)
	publish(2)

	// Resolve the stream sequence of the second envelope by walking
	// from the beginning and looking up by ID.
	allCtx, allCancel := context.WithTimeout(ctx, 10*time.Second)
	defer allCancel()
	allCh, err := cli.Subscribe(allCtx, subject, client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe (all): %v", err)
	}
	var secondSeq uint64
	for i := 0; i < 3; i++ {
		select {
		case m := <-allCh:
			if m.Envelope.ID == second.ID {
				secondSeq = m.StreamSeq
			}
			if err := m.Ack(); err != nil {
				t.Fatalf("Ack: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out reading history at i=%d", i)
		}
	}
	allCancel()
	if secondSeq == 0 {
		t.Fatal("could not resolve second envelope's stream seq")
	}

	// Subscribe from secondSeq; we must receive the 2nd and 3rd envelopes.
	resumeCtx, resumeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer resumeCancel()
	resumeCh, err := cli.SubscribeFromSeq(resumeCtx, subject, secondSeq)
	if err != nil {
		t.Fatalf("SubscribeFromSeq: %v", err)
	}

	gotIDs := make([]string, 0, 2)
	for i := 0; i < 2; i++ {
		select {
		case m := <-resumeCh:
			gotIDs = append(gotIDs, m.Envelope.ID.String())
			if err := m.Ack(); err != nil {
				t.Fatalf("Ack: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out reading resumed delivery at i=%d", i)
		}
	}
	if gotIDs[0] != second.ID.String() {
		t.Fatalf("resumed delivery first ID = %s, want %s", gotIDs[0], second.ID)
	}
}

// TestQueryReturnsNotImplementedYet pins the spec invariant: Query must
// fail fast with the M7 sentinel so callers don't think empty == "no
// history".
func TestQueryReturnsNotImplementedYet(t *testing.T) {
	srv := bootedServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cli := clientFromServer(ctx, t, srv)

	got, err := cli.Query(ctx, client.QueryFilter{})
	if err == nil {
		t.Fatal("Query must return an error in M4")
	}
	if got != nil {
		t.Fatalf("Query result = %v, want nil", got)
	}
	if !errors.Is(err, client.ErrNotImplementedYet) {
		t.Fatalf("Query error = %v, want ErrNotImplementedYet", err)
	}
	if !strings.Contains(err.Error(), "M7") {
		t.Fatalf("Query error %q must reference M7", err.Error())
	}
}

// TestWatchKVDeliversInitialAndUpdates verifies the read-only KV path:
// existing values are surfaced first, then live updates.
func TestWatchKVDeliversInitialAndUpdates(t *testing.T) {
	srv := bootedServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cli := clientFromServer(ctx, t, srv)

	// Seed initial value via the raw JS KV (no client.PutKV in M4).
	kv, err := cli.JetStream().KeyValue(ctx, "ui_state")
	if err != nil {
		t.Fatalf("KeyValue: %v", err)
	}
	if _, err := kv.Put(ctx, "operator.selected_agent", []byte("agent-1")); err != nil {
		t.Fatalf("Put initial: %v", err)
	}

	// GetKV reads the seeded value.
	val, err := cli.GetKV(ctx, "ui_state", "operator.selected_agent")
	if err != nil {
		t.Fatalf("GetKV: %v", err)
	}
	if string(val) != "agent-1" {
		t.Fatalf("GetKV value = %q, want agent-1", val)
	}

	// Missing key surfaces ErrKVKeyNotFound.
	if _, err := cli.GetKV(ctx, "ui_state", "operator.nope"); !errors.Is(err, client.ErrKVKeyNotFound) {
		t.Fatalf("GetKV missing: err = %v, want ErrKVKeyNotFound", err)
	}

	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()
	ch, err := cli.WatchKV(watchCtx, "ui_state", "operator.selected_agent")
	if err != nil {
		t.Fatalf("WatchKV: %v", err)
	}

	// First delivery: current value.
	select {
	case upd := <-ch:
		if upd.Op != client.KVOpPut {
			t.Fatalf("first update Op = %q, want put", upd.Op)
		}
		if string(upd.Value) != "agent-1" {
			t.Fatalf("first update Value = %q, want agent-1", upd.Value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out on initial KV value")
	}

	// Live update: put a new value, receive on channel.
	if _, err := kv.Put(ctx, "operator.selected_agent", []byte("agent-2")); err != nil {
		t.Fatalf("Put live: %v", err)
	}
	select {
	case upd := <-ch:
		if string(upd.Value) != "agent-2" {
			t.Fatalf("live update Value = %q, want agent-2", upd.Value)
		}
		if upd.Revision == 0 {
			t.Fatal("Revision must be populated")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out on live KV update")
	}
}

// TestSubscribeRequiresNonEmptySubject — guard against accidental "" subscriptions.
func TestSubscribeRequiresNonEmptySubject(t *testing.T) {
	srv := bootedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli := clientFromServer(ctx, t, srv)
	if _, err := cli.Subscribe(ctx, ""); err == nil {
		t.Fatal("Subscribe(\"\") must return an error")
	}
}

// TestCloseIsIdempotent ensures Close can be called twice without error.
func TestCloseIsIdempotent(t *testing.T) {
	srv := bootedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli := clientFromServer(ctx, t, srv)
	if err := cli.Close(); err != nil {
		t.Fatalf("Close (first): %v", err)
	}
	if err := cli.Close(); err != nil {
		t.Fatalf("Close (second): %v", err)
	}
	// Methods after Close must return ErrClosed.
	if _, err := cli.GetKV(ctx, "ui_state", "anything"); !errors.Is(err, client.ErrClosed) {
		t.Fatalf("GetKV after Close: err = %v, want ErrClosed", err)
	}
}

package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

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
			t.Fatal("StreamSeq must be populated — it is the resume cursor callers store and replay through SubscribeFromSeq")
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

// Query's "fail fast, never silently return an empty slice" invariant
// is enforced by the RPC layer — see TestRPCTimeoutOnNoResponder in
// rpc_test.go. The M4-era TestQueryReturnsNotImplementedYet was
// retired in M7 when Query started routing through the real RPC.

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

// TestCloseStopsLongLivedSubscribe asserts the Client tracks active
// Subscribe loops and tears them down when Close is called, even when
// the caller subscribed with a context that has not been canceled.
// This is the no-goroutine-leak invariant: STYLE.md forbids fire-and-
// forget background work.
func TestCloseStopsLongLivedSubscribe(t *testing.T) {
	srv := bootedServer(t)
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer parentCancel()
	cli := clientFromServer(parentCtx, t, srv)

	// Note: Subscribe gets context.Background(), so a caller-side
	// ctx cancel would never fire. Only Close should stop the loop.
	ch, err := cli.Subscribe(context.Background(), "agents.*.frames")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := cli.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// After Close returns, the channel must close in bounded time.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed after Close, got a value")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe channel did not close within 5s after Client.Close — goroutine leak")
	}
}

// TestCloseStopsLongLivedWatchKV is the WatchKV counterpart: Close must
// tear down a watcher loop that was started against a long-lived ctx.
func TestCloseStopsLongLivedWatchKV(t *testing.T) {
	srv := bootedServer(t)
	parentCtx, parentCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer parentCancel()
	cli := clientFromServer(parentCtx, t, srv)

	ch, err := cli.WatchKV(context.Background(), "ui_state", "test.key")
	if err != nil {
		t.Fatalf("WatchKV: %v", err)
	}

	if err := cli.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case _, ok := <-ch:
		if ok {
			// A real KV update could arrive before Close drains it.
			// Drain until the channel closes.
			drainUntilClosed(t, ch, 5*time.Second)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WatchKV channel did not close within 5s after Client.Close — goroutine leak")
	}
}

// drainUntilClosed reads from a generic-shape channel until it closes
// or the budget runs out.
func drainUntilClosed[T any](t *testing.T, ch <-chan T, budget time.Duration) {
	t.Helper()
	deadline := time.After(budget)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("channel did not close within budget")
		}
	}
}

// TestSubscribeSurfacesMalformedEnvelopeError exercises the spec's
// "type validation: returned as an error to the caller, not silently
// coerced" contract. A non-JSON payload published into the stream must
// arrive on the channel as a Message with Err set and the JetStream
// stream-seq populated (so callers can correlate / resume), while the
// underlying message is Term'd so JetStream does not redeliver it.
func TestSubscribeSurfacesMalformedEnvelopeError(t *testing.T) {
	srv := bootedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli := clientFromServer(ctx, t, srv)

	subject := "agents.cccccccc-dddd-eeee-ffff-aaaaaaaaaaaa.frames"
	ch, err := cli.Subscribe(ctx, subject, client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Publish raw bytes that are not a JSON envelope.
	if _, err := cli.JetStream().Publish(ctx, subject, []byte("not a json envelope")); err != nil {
		t.Fatalf("JS Publish: %v", err)
	}

	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatal("subscription channel closed before delivery")
		}
		if msg.Err == nil {
			t.Fatal("Message.Err must be non-nil for malformed payloads")
		}
		if msg.Subject != subject {
			t.Fatalf("Subject = %q, want %q", msg.Subject, subject)
		}
		if msg.StreamSeq == 0 {
			t.Fatal("StreamSeq must be populated on error Messages so callers can resume past the bad envelope")
		}
		// Envelope must be the zero value when Err is set.
		// (Envelope contains json.RawMessage which is not directly
		// comparable; check the load-bearing identity fields instead.)
		if msg.Envelope.ID != uuid.Nil {
			t.Fatalf("Envelope.ID = %s, want zero (uuid.Nil)", msg.Envelope.ID)
		}
		if msg.Envelope.Kind != "" {
			t.Fatalf("Envelope.Kind = %q, want empty", msg.Envelope.Kind)
		}
		if msg.Envelope.Payload != nil {
			t.Fatalf("Envelope.Payload = %q, want nil", msg.Envelope.Payload)
		}
		// Ack on an error Message is a no-op (returns nil).
		if err := msg.Ack(); err != nil {
			t.Fatalf("noop Ack on error Message returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for malformed-envelope delivery")
	}
}

// TestAckIsSafeToCallMultipleTimes pins the contract on Message.Ack —
// the underlying JetStream ack fires exactly once and subsequent calls
// return nil, so callers can stash and replay Messages without tracking
// ack state externally.
func TestAckIsSafeToCallMultipleTimes(t *testing.T) {
	srv := bootedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cli := clientFromServer(ctx, t, srv)

	subject := "agents.dddddddd-eeee-ffff-aaaa-bbbbbbbbbbbb.frames"
	ch, err := cli.Subscribe(ctx, subject, client.WithDeliverAll())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: "dddddddd-eeee-ffff-aaaa-bbbbbbbbbbbb"}
	env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from, map[string]string{"k": "v"})
	if err != nil {
		t.Fatalf("NewEnvelopeWith: %v", err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if _, err := cli.JetStream().Publish(ctx, subject, raw); err != nil {
		t.Fatalf("JS Publish: %v", err)
	}

	select {
	case msg := <-ch:
		if err := msg.Ack(); err != nil {
			t.Fatalf("Ack (first): %v", err)
		}
		// Second Ack must return nil — the contract.
		if err := msg.Ack(); err != nil {
			t.Fatalf("Ack (second) returned %v, want nil", err)
		}
		// Third for good measure.
		if err := msg.Ack(); err != nil {
			t.Fatalf("Ack (third) returned %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

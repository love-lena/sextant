package client_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/client"
	"github.com/love-lena/sextant/pkg/natsboot"
	"github.com/love-lena/sextant/pkg/sextantproto"
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

// seedJS publishes raw to subject via the privileged DAEMON principal.
// Since feat-ctl-f0 the broker-scoped operator credential the test client
// uses may publish only sextant.rpc.* + reply inboxes — it cannot publish
// to agents.*.frames. Tests that need to seed an agent stream (to then
// exercise the operator-side *read* path) must publish as the daemon,
// which mirrors production where the sidecar/daemon are the only frame
// publishers.
func seedJS(ctx context.Context, t *testing.T, srv *natsboot.Server, subject string, raw []byte) {
	t.Helper()
	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("seedJS: daemon connect: %v", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("seedJS: jetstream.New: %v", err)
	}
	if _, err := js.Publish(ctx, subject, raw); err != nil {
		t.Fatalf("seedJS: publish %s: %v", subject, err)
	}
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
	// Seed via the daemon principal — the operator client may not publish
	// to agents.*.frames (feat-ctl-f0).
	seedJS(ctx, t, srv, subject, raw)

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
		// Seed via the daemon principal (feat-ctl-f0): operator can't
		// publish agents.*.frames.
		seedJS(ctx, t, srv, subject, raw)
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

	// Publish raw bytes that are not a JSON envelope, via the daemon
	// principal (feat-ctl-f0): operator can't publish agents.*.frames.
	seedJS(ctx, t, srv, subject, []byte("not a json envelope"))

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

// writeClientToml writes a client.toml at path pointing at natsURL with
// inline operator user/password. Helper for the runtime-override tests
// below.
func writeClientToml(t *testing.T, path, natsURL, user, password string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	body := fmt.Sprintf(`
[nats]
url = %q

[operator]
user = %q
password = %q
`, natsURL, user, password)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// writeRuntimeJSON writes a runtime.json at path with the given
// nats_addr (host:port, no scheme — matching the sextantd shape).
func writeRuntimeJSON(t *testing.T, path, natsAddr string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(path), err)
	}
	body := fmt.Sprintf(`{"nats_addr":%q,"clickhouse_tcp":"127.0.0.1:9000"}`, natsAddr)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// staleClientTomlAddr is the placeholder URL `sextant init` writes —
// always wrong on a real install where sextantd picks an auto-allocated
// port. The runtime-override tests use this as the "stale" URL: a
// successful Connect proves the runtime.json override fired, since a
// dial to this address would never reach the booted test server.
const staleClientTomlAddr = "nats://127.0.0.1:14222"

// TestConnectPrefersRuntimeJSONOverStaleClientToml is the load-bearing
// acceptance for bug-clienttoml-stale-port-on-restart. Setup:
//   - real nats-server booted on an auto-allocated port (the "live" port)
//   - client.toml on disk points at a stale port that is NOT the server
//   - runtime.json on disk points at the live port
//
// Connect must dial the live port (from runtime.json), not the stale
// port (from client.toml). Before the fix, this test would hang on the
// connect timeout.
func TestConnectPrefersRuntimeJSONOverStaleClientToml(t *testing.T) {
	srv := bootedServer(t)

	// Redirect $HOME so DefaultConfigPath and DefaultRuntimePath both
	// resolve under our tempdir. os.UserHomeDir respects HOME on Unix.
	home := t.TempDir()
	t.Setenv("HOME", home)

	clientTomlPath := filepath.Join(home, ".config", "sextant", "client.toml")
	writeClientToml(t, clientTomlPath, staleClientTomlAddr, srv.OperatorUser(), srv.OperatorPassword())

	runtimePath := filepath.Join(home, ".local", "share", "sextant", "runtime.json")
	writeRuntimeJSON(t, runtimePath, srv.Address())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cli, err := client.Connect(ctx, "")
	if err != nil {
		t.Fatalf("Connect: %v (runtime.json override should have steered to the live port)", err)
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	// Sanity: the Client's recorded URL must be the live one — that's
	// the field validateAndFill stamped, and what nats.Connect dialed.
	if got, want := cli.Config().NATS.URL, srv.PublicURL(); got != want {
		t.Fatalf("Client.Config().NATS.URL = %q, want %q (runtime.json override didn't apply)", got, want)
	}

	// And the connection is actually live.
	if !cli.Conn().IsConnected() {
		t.Fatal("Client connected but underlying *nats.Conn is not connected — runtime.json override returned a wrong address")
	}
}

// TestConnectUsesClientTomlWhenRuntimeJSONAbsent — the no-daemon case.
// runtime.json doesn't exist (e.g. operator running `sextant` before
// `sextantd` ever started). Connect must fall back to client.toml's URL.
// We point client.toml at the live server so the connect succeeds; if
// the helper accidentally errored on the missing file, Connect would
// fail.
func TestConnectUsesClientTomlWhenRuntimeJSONAbsent(t *testing.T) {
	srv := bootedServer(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// client.toml: live URL. runtime.json: NOT written.
	clientTomlPath := filepath.Join(home, ".config", "sextant", "client.toml")
	writeClientToml(t, clientTomlPath, srv.PublicURL(), srv.OperatorUser(), srv.OperatorPassword())

	runtimePath := filepath.Join(home, ".local", "share", "sextant", "runtime.json")
	if _, err := os.Stat(runtimePath); !os.IsNotExist(err) {
		t.Fatalf("test setup: runtime.json should be absent, stat err=%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cli, err := client.Connect(ctx, "")
	if err != nil {
		t.Fatalf("Connect: %v (should have used client.toml URL when runtime.json absent)", err)
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	if got, want := cli.Config().NATS.URL, srv.PublicURL(); got != want {
		t.Fatalf("Client.Config().NATS.URL = %q, want %q (Connect didn't fall back to client.toml)", got, want)
	}
}

// TestConnectFallsBackToClientTomlOnMalformedRuntimeJSON — the daemon
// crashed mid-write or someone hand-edited runtime.json into nonsense.
// Connect must NOT crash, NOT error; it must transparently fall back to
// client.toml. This is the "don't make a degraded daemon worse" contract.
func TestConnectFallsBackToClientTomlOnMalformedRuntimeJSON(t *testing.T) {
	srv := bootedServer(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// client.toml: live URL (so the fallback dial succeeds).
	clientTomlPath := filepath.Join(home, ".config", "sextant", "client.toml")
	writeClientToml(t, clientTomlPath, srv.PublicURL(), srv.OperatorUser(), srv.OperatorPassword())

	// runtime.json: garbage.
	runtimePath := filepath.Join(home, ".local", "share", "sextant", "runtime.json")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(runtimePath, []byte("{half-written"), 0o600); err != nil {
		t.Fatalf("WriteFile runtime: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cli, err := client.Connect(ctx, "")
	if err != nil {
		t.Fatalf("Connect: %v (malformed runtime.json must not propagate; expected silent fallback to client.toml)", err)
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	if got, want := cli.Config().NATS.URL, srv.PublicURL(); got != want {
		t.Fatalf("Client.Config().NATS.URL = %q, want %q (Connect should have fallen back to client.toml)", got, want)
	}
}

// TestConnectRuntimeJSONWinsOverDifferingClientToml is the "both
// present, different ports" case. The point of Option B is that
// runtime.json is authoritative whenever it parses cleanly, so a stale
// client.toml never matters — even if it points at a port that would
// connect to something else entirely.
func TestConnectRuntimeJSONWinsOverDifferingClientToml(t *testing.T) {
	srv := bootedServer(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	// Stale client.toml URL distinct from the live one.
	clientTomlPath := filepath.Join(home, ".config", "sextant", "client.toml")
	writeClientToml(t, clientTomlPath, staleClientTomlAddr, srv.OperatorUser(), srv.OperatorPassword())

	// runtime.json points at the live server.
	runtimePath := filepath.Join(home, ".local", "share", "sextant", "runtime.json")
	writeRuntimeJSON(t, runtimePath, srv.Address())

	// Sanity: confirm they disagree, so the test is meaningful.
	if srv.PublicURL() == staleClientTomlAddr {
		t.Fatalf("test setup invariant: live URL %q must differ from stale %q",
			srv.PublicURL(), staleClientTomlAddr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cli, err := client.Connect(ctx, "")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer cli.Close() //nolint:errcheck // best-effort close

	if got, want := cli.Config().NATS.URL, srv.PublicURL(); got != want {
		t.Fatalf("Client.Config().NATS.URL = %q, want %q (runtime.json should have won over client.toml)", got, want)
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
	// Seed via the daemon principal (feat-ctl-f0): operator can't publish
	// agents.*.frames.
	seedJS(ctx, t, srv, subject, raw)

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

package rpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant-initial/pkg/client"
	"github.com/love-lena/sextant-initial/pkg/natsboot"
	"github.com/love-lena/sextant-initial/pkg/rpc"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// requireNATSBin skips the test when nats-server is not on PATH.
func requireNATSBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}
	return p
}

// bootedServer brings up nats-server with the sextant JetStream layout
// applied. The subprocess is bound to context.Background() so it
// outlives this helper; t.Cleanup stops it on test exit.
func bootedServer(t *testing.T) *natsboot.Server {
	t.Helper()
	bin := requireNATSBin(t)
	dir := t.TempDir()
	cfg := natsboot.DefaultConfig(filepath.Join(dir, "nats"))
	cfg.NATSBinary = bin

	srv, err := natsboot.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("natsboot.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})
	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("srv.Connect: %v", err)
	}
	defer nc.Close()
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer bootCancel()
	if err := natsboot.Bootstrap(bootCtx, nc, cfg.MaxBytesPerStream); err != nil {
		t.Fatalf("natsboot.Bootstrap: %v", err)
	}
	return srv
}

// operatorConn opens a raw operator connection — used by tests that
// need to call NATS directly (publishing the synthetic audit
// subscription, etc.).
func operatorConn(t *testing.T, srv *natsboot.Server) *nats.Conn {
	t.Helper()
	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("operator connect: %v", err)
	}
	t.Cleanup(func() { nc.Close() })
	return nc
}

// newServerOnConn wires an rpc.Server against nc and starts its Run
// loop. t.Cleanup tears it down. The returned cancel cancels the Run
// loop ahead of t.Cleanup if a test needs to.
func newServerOnConn(t *testing.T, nc *nats.Conn) (*rpc.Server, context.CancelFunc) {
	t.Helper()
	srv, err := rpc.New(nc, rpc.Config{
		From: sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "test"},
	})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
	})
	// Give Run a moment to subscribe before tests fire requests.
	time.Sleep(50 * time.Millisecond)
	return srv, cancel
}

// clientOn wraps client.ConnectWithConfig against srv.
func clientOn(t *testing.T, srv *natsboot.Server) *client.Client {
	t.Helper()
	cfg := client.Config{
		NATS:     client.NATSConfig{URL: srv.PublicURL()},
		Operator: client.OperatorConfig{User: srv.OperatorUser(), Password: srv.OperatorPassword()},
	}
	cli, err := client.ConnectWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("client.ConnectWithConfig: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

// TestServerRoundTripsViaClient covers the happy path: register a
// handler, call it via pkg/client.RPC, get the expected reply.
func TestServerRoundTripsViaClient(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s, _ := newServerOnConn(t, nc)
	if err := s.Register("ping", func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		emit(sextantproto.RPCResponse{
			Result:   json.RawMessage(`{"pong":true}`),
			Terminal: true,
		})
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cli := clientOn(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var got struct {
		Pong bool `json:"pong"`
	}
	if err := cli.RPC(ctx, "ping", map[string]string{}, &got); err != nil {
		t.Fatalf("RPC: %v", err)
	}
	if !got.Pong {
		t.Fatal("Pong = false; want true")
	}
}

// TestServerIdempotencyReplay asserts a repeat (verb, idempotency_key)
// returns the cached reply without re-executing the handler.
func TestServerIdempotencyReplay(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s, _ := newServerOnConn(t, nc)

	var hits int64
	if err := s.Register("count", func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		n := atomic.AddInt64(&hits, 1)
		result, _ := json.Marshal(map[string]int64{"hit": n})
		emit(sextantproto.RPCResponse{Result: result, Terminal: true})
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cli := clientOn(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const key = "00000000-0000-0000-0000-aaaaaaaaaaaa"
	var first, second struct {
		Hit int64 `json:"hit"`
	}
	if err := cli.RPC(ctx, "count", nil, &first, client.WithIdempotencyKey(key)); err != nil {
		t.Fatalf("RPC 1: %v", err)
	}
	if err := cli.RPC(ctx, "count", nil, &second, client.WithIdempotencyKey(key)); err != nil {
		t.Fatalf("RPC 2: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("handler ran %d times, want 1 (idempotency replay)", got)
	}
	if first.Hit != 1 || second.Hit != 1 {
		t.Fatalf("first=%d second=%d; both must be 1 (the cached value)", first.Hit, second.Hit)
	}
}

// TestServerTimesOutSlowHandler asserts the server emits a structured
// timeout reply when the handler exceeds the configured timeout.
func TestServerTimesOutSlowHandler(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)

	s, err := rpc.New(nc, rpc.Config{
		From:           sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "test"},
		HandlerTimeout: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	runCtx, cancelRun := context.WithCancel(context.Background())
	go func() { _ = s.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		_ = s.Close()
	})
	time.Sleep(50 * time.Millisecond)

	if err := s.Register("slow", func(ctx context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		select {
		case <-time.After(2 * time.Second):
			emit(sextantproto.RPCResponse{Result: json.RawMessage(`{}`), Terminal: true})
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cli := clientOn(t, srv)
	// Give the client a longer per-call timeout than the handler — we
	// want the server's emission of the timeout reply, not the client
	// giving up first.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = cli.RPC(ctx, "slow", nil, nil, client.WithTimeout(5*time.Second))
	if err == nil {
		t.Fatal("RPC must return error after server-side timeout")
	}
	var rerr *client.RPCError
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *client.RPCError carrying timeout", err)
	}
	if rerr.Code != sextantproto.ErrCodeTimeout {
		t.Fatalf("Code = %q, want %q", rerr.Code, sextantproto.ErrCodeTimeout)
	}
}

// TestServerUnknownVerbReturnsRPCError covers the dispatch path when
// no handler is registered.
func TestServerUnknownVerbReturnsRPCError(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	_, _ = newServerOnConn(t, nc)

	cli := clientOn(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := cli.RPC(ctx, "no_such_verb", nil, nil)
	if err == nil {
		t.Fatal("RPC must return an error for unknown verb")
	}
	var rerr *client.RPCError
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *client.RPCError", err)
	}
	if rerr.Code != sextantproto.ErrCodeUnknownVerb {
		t.Fatalf("Code = %q, want %q", rerr.Code, sextantproto.ErrCodeUnknownVerb)
	}
}

// TestServerAuditEnvelopesPublished asserts the spec-required audit
// pair (audit.rpc + audit.rpc_result) fires per RPC and carries the
// request's trace_id.
func TestServerAuditEnvelopesPublished(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s, _ := newServerOnConn(t, nc)
	if err := s.Register("ok", func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		emit(sextantproto.RPCResponse{Result: json.RawMessage(`{"ok":true}`), Terminal: true})
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Subscribe to both audit subjects before firing the RPC. We use a
	// raw operator connection — the audit envelopes go out over plain
	// NATS publish, then the JetStream `audit` stream persists them.
	auditCh := make(chan *nats.Msg, 8)
	rpcSub, err := nc.ChanSubscribe("audit.rpc", auditCh)
	if err != nil {
		t.Fatalf("subscribe audit.rpc: %v", err)
	}
	defer func() { _ = rpcSub.Unsubscribe() }()
	resultCh := make(chan *nats.Msg, 8)
	resSub, err := nc.ChanSubscribe("audit.rpc_result", resultCh)
	if err != nil {
		t.Fatalf("subscribe audit.rpc_result: %v", err)
	}
	defer func() { _ = resSub.Unsubscribe() }()

	cli := clientOn(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := cli.RPC(ctx, "ok", nil, nil); err != nil {
		t.Fatalf("RPC: %v", err)
	}

	pre := waitForOneAudit(t, auditCh, 3*time.Second, "audit.rpc")
	post := waitForOneAudit(t, resultCh, 3*time.Second, "audit.rpc_result")

	var zero [16]byte
	if pre.TraceID == zero || post.TraceID == zero {
		t.Fatal("trace_id must be non-zero on audit envelopes")
	}
	if pre.TraceID != post.TraceID {
		t.Fatalf("audit pair must share trace_id (got pre=%s post=%s)", pre.TraceID, post.TraceID)
	}
}

// waitForOneAudit reads one envelope off ch, asserts it decodes, and
// returns it.
func waitForOneAudit(t *testing.T, ch <-chan *nats.Msg, timeout time.Duration, label string) sextantproto.Envelope {
	t.Helper()
	select {
	case msg, ok := <-ch:
		if !ok {
			t.Fatalf("%s subscription closed", label)
		}
		var env sextantproto.Envelope
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			t.Fatalf("%s decode: %v", label, err)
		}
		if env.Kind != sextantproto.KindAudit {
			t.Fatalf("%s envelope kind = %q, want audit", label, env.Kind)
		}
		return env
	case <-time.After(timeout):
		t.Fatalf("%s: no envelope within %s", label, timeout)
	}
	return sextantproto.Envelope{}
}

// TestServerHandlerErrorReachesCaller covers a handler that emits a
// structured error: the client receives it as *RPCError with the same
// code/message.
func TestServerHandlerErrorReachesCaller(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s, _ := newServerOnConn(t, nc)
	if err := s.Register("oops", func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		emit(sextantproto.RPCResponse{
			Error:    &sextantproto.RPCError{Code: sextantproto.ErrCodeBadRequest, Message: "no good"},
			Terminal: true,
		})
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cli := clientOn(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := cli.RPC(ctx, "oops", nil, nil)
	var rerr *client.RPCError
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *client.RPCError", err)
	}
	if rerr.Code != sextantproto.ErrCodeBadRequest {
		t.Fatalf("Code = %q, want %q", rerr.Code, sextantproto.ErrCodeBadRequest)
	}
	if rerr.Message != "no good" {
		t.Fatalf("Message = %q, want %q", rerr.Message, "no good")
	}
}

// TestServerDoubleRegisterFails pins the contract that re-registering
// is an error, not silent shadowing.
func TestServerDoubleRegisterFails(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s, _ := newServerOnConn(t, nc)
	noop := func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		emit(sextantproto.RPCResponse{Result: json.RawMessage(`{}`), Terminal: true})
		return nil
	}
	if err := s.Register("v", noop); err != nil {
		t.Fatalf("Register 1: %v", err)
	}
	if err := s.Register("v", noop); err == nil {
		t.Fatal("Register 2 must reject duplicate verb")
	}
}

// TestServerRejectsMissingIdempotencyKey checks the spec rule that
// every request carries an idempotency key.
func TestServerRejectsMissingIdempotencyKey(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	_, _ = newServerOnConn(t, nc)

	// Bypass pkg/client.RPC (which always generates a key) by
	// publishing a hand-built envelope.
	reply := nats.NewInbox()
	ch := make(chan *nats.Msg, 1)
	sub, err := nc.ChanSubscribe(reply, ch)
	if err != nil {
		t.Fatalf("subscribe reply: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	from := sextantproto.Address{Kind: sextantproto.AddressOperator, ID: "lena"}
	env := sextantproto.NewEnvelope(sextantproto.KindRPCRequest, from, json.RawMessage(`{}`))
	env.ReplyTo = &reply
	// Intentionally NO IdempotencyKey.
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := nc.Publish("sextant.rpc.whatever", raw); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case msg := <-ch:
		var respEnv sextantproto.Envelope
		if err := json.Unmarshal(msg.Data, &respEnv); err != nil {
			t.Fatalf("decode reply: %v", err)
		}
		var resp sextantproto.RPCResponse
		if err := json.Unmarshal(respEnv.Payload, &resp); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if resp.Error == nil {
			t.Fatal("expected RPC error, got success")
		}
		if resp.Error.Code != sextantproto.ErrCodeBadRequest {
			t.Fatalf("Code = %q, want %q", resp.Error.Code, sextantproto.ErrCodeBadRequest)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no reply within 3s")
	}
}

// TestServerCloseDrainsInFlight ensures Close blocks until in-flight
// handlers finish.
func TestServerCloseDrainsInFlight(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)

	s, err := rpc.New(nc, rpc.Config{
		From:           sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "test"},
		HandlerTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	runCtx, runCancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(runCtx) }()
	time.Sleep(50 * time.Millisecond)

	released := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	if err := s.Register("slow", func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		defer wg.Done()
		time.Sleep(300 * time.Millisecond)
		emit(sextantproto.RPCResponse{Result: json.RawMessage(`{}`), Terminal: true})
		close(released)
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cli := clientOn(t, srv)
	go func() {
		// Fire & forget — we just care that the handler ran to
		// completion before Close returns.
		_ = cli.RPC(context.Background(), "slow", nil, nil, client.WithTimeout(2*time.Second))
	}()
	time.Sleep(100 * time.Millisecond) // let the dispatch land

	runCancel()
	closeStart := time.Now()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closeDur := time.Since(closeStart)
	// Close must wait for the handler — we should have seen `released`.
	select {
	case <-released:
	default:
		t.Fatalf("Close returned before handler finished (took %s)", closeDur)
	}
	wg.Wait()
}

// TestServerCacheExpires asserts that after the configured TTL, the
// same (verb, key) re-executes.
func TestServerCacheExpires(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s, err := rpc.New(nc, rpc.Config{
		From:           sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "test"},
		IdempotencyTTL: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Close()
	})
	time.Sleep(50 * time.Millisecond)

	var hits int64
	if err := s.Register("expire", func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		n := atomic.AddInt64(&hits, 1)
		result, _ := json.Marshal(map[string]int64{"n": n})
		emit(sextantproto.RPCResponse{Result: result, Terminal: true})
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cli := clientOn(t, srv)
	ctx, cancelCtx := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelCtx()
	const key = "11111111-2222-3333-4444-555555555555"
	if err := cli.RPC(ctx, "expire", nil, nil, client.WithIdempotencyKey(key)); err != nil {
		t.Fatalf("RPC 1: %v", err)
	}
	// Sleep past TTL to let the entry expire.
	time.Sleep(400 * time.Millisecond)
	if err := cli.RPC(ctx, "expire", nil, nil, client.WithIdempotencyKey(key)); err != nil {
		t.Fatalf("RPC 2: %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("handler ran %d times, want 2 (cache should have expired)", got)
	}
}

// TestCapDenyRoutesThroughCapability covers the capability gate.
// Replaces the AllowAll default with a checker that denies — the
// client sees a capability_denied RPCError.
func TestCapDenyRoutesThroughCapability(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s, err := rpc.New(nc, rpc.Config{
		From:       sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "test"},
		CapChecker: denyAll{},
	})
	if err != nil {
		t.Fatalf("rpc.New: %v", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	go func() { _ = s.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Close()
	})
	time.Sleep(50 * time.Millisecond)
	if err := s.Register("anything", func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		emit(sextantproto.RPCResponse{Result: json.RawMessage(`{}`), Terminal: true})
		return nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cli := clientOn(t, srv)
	ctx, cancelCtx := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCtx()
	err = cli.RPC(ctx, "anything", nil, nil)
	var rerr *client.RPCError
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *client.RPCError", err)
	}
	if rerr.Code != sextantproto.ErrCodeCapabilityDenied {
		t.Fatalf("Code = %q, want %q", rerr.Code, sextantproto.ErrCodeCapabilityDenied)
	}
	// Details["capability_required"] must carry the cap name so M10
	// operator tooling can render "missing capability X" without
	// parsing the message string.
	if got := rerr.Details["capability_required"]; got == nil {
		t.Fatalf("Details.capability_required missing; Details = %+v", rerr.Details)
	}
}

type denyAll struct{}

func (denyAll) Check(_ sextantproto.Envelope, cap string) error {
	return fmt.Errorf("denied: %s", cap)
}

// TestServerRecoversFromHandlerPanic pins the spec-required behavior:
// a panicking handler must not crash the daemon, and the caller must
// receive a terminal RPCError{Code: ErrCodeInternal} so it isn't left
// waiting for a reply that never comes (the wire-semantics rule
// "missing reply is a protocol violation").
func TestServerRecoversFromHandlerPanic(t *testing.T) {
	srv := bootedServer(t)
	nc := operatorConn(t, srv)
	s, _ := newServerOnConn(t, nc)
	if err := s.Register("boom", func(_ context.Context, _ sextantproto.Envelope, _ func(sextantproto.RPCResponse)) error {
		panic("synthetic handler crash")
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cli := clientOn(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := cli.RPC(ctx, "boom", nil, nil, client.WithTimeout(3*time.Second))
	if err == nil {
		t.Fatal("RPC must return an error when the server-side handler panics")
	}
	if errors.Is(err, client.ErrRPCTimeout) {
		t.Fatalf("expected structured RPCError, got client-side ErrRPCTimeout — server crashed instead of recovering")
	}
	var rerr *client.RPCError
	if !errors.As(err, &rerr) {
		t.Fatalf("err = %v, want *client.RPCError", err)
	}
	if rerr.Code != sextantproto.ErrCodeInternal {
		t.Fatalf("Code = %q, want %q", rerr.Code, sextantproto.ErrCodeInternal)
	}
	if rerr.Details["panic"] == nil {
		t.Fatalf("Details.panic missing; Details = %+v", rerr.Details)
	}

	// The server must still be alive — issue a second RPC against a
	// freshly-registered healthy handler to confirm the daemon didn't
	// crash.
	if err := s.Register("ok2", func(_ context.Context, _ sextantproto.Envelope, emit func(sextantproto.RPCResponse)) error {
		emit(sextantproto.RPCResponse{Result: []byte(`{"ok":true}`), Terminal: true})
		return nil
	}); err != nil {
		t.Fatalf("Register ok2: %v", err)
	}
	if err := cli.RPC(ctx, "ok2", nil, nil); err != nil {
		t.Fatalf("RPC after panic: server did not survive (%v)", err)
	}
}

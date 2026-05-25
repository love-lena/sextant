package mcpserver

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestRunToolHandlerRecoversPanic exercises the dispatcher's panic
// recovery directly: a handler that panics returns a clean toolError
// (code = internal, details.panic = recovered value) instead of
// crashing the goroutine.
//
// This pairs with the higher-level acceptance below; together they
// pin the contract that one bad tool implementation cannot take the
// MCP server down.
func TestRunToolHandlerRecoversPanic(t *testing.T) {
	// A bare Server is enough — runToolHandler only reads s.logger.
	srv := &Server{logger: log.Default()}
	caller := Caller{Kind: CallerAgent, AgentUUID: uuid.New()}

	out, err := runToolHandler(srv, "panicky_tool", context.Background(), caller, struct{}{},
		func(_ context.Context, _ Caller, _ struct{}) (any, error) {
			panic("intentional boom")
		})

	if out != nil {
		t.Errorf("out = %v, want nil on panic", out)
	}
	if err == nil {
		t.Fatalf("expected toolError; got nil")
	}
	var te toolError
	if !errors.As(err, &te) {
		t.Fatalf("err = %T, want toolError", err)
	}
	if te.Code != sextantproto.ErrCodeInternal {
		t.Errorf("Code = %q, want %q", te.Code, sextantproto.ErrCodeInternal)
	}
	if te.Details["panic"] != "intentional boom" {
		t.Errorf("Details[panic] = %v, want %q", te.Details["panic"], "intentional boom")
	}
	if te.Details["tool"] != "panicky_tool" {
		t.Errorf("Details[tool] = %v, want panicky_tool", te.Details["tool"])
	}
}

// TestAcceptLoopSurvivesTransientError pins the contract that a
// transient accept error (think EMFILE/EAGAIN) does NOT kill the
// stdio loop. The loop must log, back off, and keep accepting so that
// once the fd table recovers, new clients can connect again. The
// previous "return on any non-ErrClosed error" behavior silently
// dropped stdio for the rest of the daemon's life.
func TestAcceptLoopSurvivesTransientError(t *testing.T) {
	srv := &Server{logger: log.Default()}

	// Sequence of accept results: transient, transient, success,
	// transient, ErrClosed. The loop must run dispatch exactly once,
	// then exit cleanly when ErrClosed lands.
	var calls atomic.Int32
	transient := errors.New("simulated EMFILE")
	accept := func() (*net.UnixConn, error) {
		n := calls.Add(1)
		switch n {
		case 1, 2, 4:
			return nil, transient
		case 3:
			// Successful accept — synthesize a real *net.UnixConn via
			// a local listen+dial pair so the loop receives a
			// concrete type matching its signature.
			conn, err := newUnixSocketPair(t)
			if err != nil {
				return nil, err
			}
			return conn, nil
		default:
			return nil, net.ErrClosed
		}
	}

	var dispatches atomic.Int32
	dispatch := func(_ context.Context, conn *net.UnixConn) {
		dispatches.Add(1)
		if conn != nil {
			_ = conn.Close()
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run the loop. Returns when accept yields net.ErrClosed.
	done := make(chan struct{})
	go func() {
		srv.acceptLoop(ctx, accept, dispatch)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("acceptLoop did not return on ErrClosed within 3s")
	}

	if got := calls.Load(); got != 5 {
		t.Errorf("accept calls = %d, want 5 (3 transients + 1 ok + 1 ErrClosed)", got)
	}
	if got := dispatches.Load(); got != 1 {
		t.Errorf("dispatch calls = %d, want 1 (only one successful accept)", got)
	}
	srv.stdioWG.Wait()
}

// newUnixSocketPair binds a local unix socket and dials it, returning
// the dialer side as a *net.UnixConn so the test's accept stub can
// satisfy the *net.UnixConn return type. Both sides are closed via
// t.Cleanup so no fds leak.
func newUnixSocketPair(t *testing.T) (*net.UnixConn, error) {
	t.Helper()
	sock := shortInternalSocketPath(t)
	addr, err := net.ResolveUnixAddr("unix", sock)
	if err != nil {
		return nil, err
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = ln.Close() })

	type res struct {
		conn *net.UnixConn
		err  error
	}
	accepted := make(chan res, 1)
	go func() {
		c, err := ln.AcceptUnix()
		accepted <- res{conn: c, err: err}
	}()
	dialed, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = dialed.Close() })
	r := <-accepted
	if r.err != nil {
		return nil, r.err
	}
	t.Cleanup(func() { _ = r.conn.Close() })
	return dialed, nil
}

// shortInternalSocketPath returns a /tmp-rooted unix socket path that
// fits within macOS' 104-byte sun_path cap. Mirrors the helper of the
// same shape in server_test.go but lives in the internal package.
func shortInternalSocketPath(t *testing.T) string {
	t.Helper()
	id := uuid.New().String()[:12]
	p := "/tmp/sxt-mcp-int-" + id + ".sock"
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

// TestWrapHandlerSurvivesPanic checks the full dispatch chain: a
// wrapHandler-wrapped panicking tool returns a CallToolResult with
// IsError=true and a structured body — no propagated protocol error,
// no goroutine crash. Audit publish is exercised via the same path
// (nc=nil short-circuits inside auditPublisher.publish so the test
// stays standalone).
func TestWrapHandlerSurvivesPanic(t *testing.T) {
	srv := &Server{
		logger: log.Default(),
		audit:  &auditPublisher{nc: nil, logger: log.Default()},
	}
	wrapped := wrapHandler[struct{}](srv, ToolSendMessage,
		func(_ context.Context, _ Caller, _ struct{}) (any, error) {
			panic("boom")
		})

	res, out, err := wrapped(context.Background(), nil, struct{}{})
	if err != nil {
		t.Fatalf("wrapHandler must not propagate protocol errors; got %v", err)
	}
	if out != nil {
		t.Errorf("out = %v, want nil on panic", out)
	}
	if res == nil {
		t.Fatalf("res = nil; want CallToolResult with IsError")
	}
	if !res.IsError {
		t.Errorf("IsError = false; want true")
	}
	// Body should carry our structured error.
	body, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent = %T; want map[string]any", res.StructuredContent)
	}
	if body["code"] != sextantproto.ErrCodeInternal {
		t.Errorf("code = %v, want %q", body["code"], sextantproto.ErrCodeInternal)
	}
}

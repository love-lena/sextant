package mcpserver_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/love-lena/sextant-initial/pkg/mcpserver"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestShutdownForcesCloseOnLongLivedSSEStream pins the contract that
// daemon shutdown completes in bounded time even when a sidecar is
// holding open a long-lived streamable-HTTP SSE stream. Without the
// Shutdown→Close fallback, graceful Shutdown blocks until the client
// disconnects (which on a real daemon shutdown is "never"), leaking
// goroutines.
//
// Strategy: open a Streamable HTTP client *with* the standalone SSE
// stream (the SDK default), confirm at least one request succeeded,
// then call Close on the server and assert it returns within a window
// that includes the graceful 5s grace + a small margin.
func TestShutdownForcesCloseOnLongLivedSSEStream(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)

	// Build the server by hand so we control the lifecycle precisely
	// (the shared fixture cancels via t.Cleanup which runs LIFO; here
	// we want to time Close from the call site).
	nc, err := natsSrv.Connect()
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(func() { nc.Close() })

	sockPath := shortSocketPath(t)

	srv, err := mcpserver.New(mcpserver.Config{
		NATS:        nc,
		CA:          ca,
		From:        sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "shutdown-test"},
		HTTPHost:    "127.0.0.1",
		HTTPPort:    0,
		StdioSocket: sockPath,
	})
	if err != nil {
		t.Fatalf("mcpserver.New: %v", err)
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = srv.Run(runCtx)
	}()
	select {
	case <-srv.Ready():
	case <-time.After(10 * time.Second):
		t.Fatalf("mcpserver: not ready within 10s")
	}

	// Open an MCP client that DOES establish the standalone SSE
	// stream. This is the path that historically blocks Shutdown.
	token, _ := issueJWT(t, ca, []string{mcpserver.CapSendMessage})
	transport := &mcp.StreamableClientTransport{
		Endpoint: srv.HTTPURL(),
		HTTPClient: &http.Client{
			Transport: &bearerRoundTripper{token: token, base: http.DefaultTransport},
			// Long timeout so the SSE stream isn't client-side
			// short-circuited; we want the server-side shutdown path
			// to be the deciding factor.
			Timeout: 0,
		},
		// DisableStandaloneSSE: false is the SDK default — leave it so
		// the GET-SSE stream gets established.
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "shutdown-test-client", Version: "0.0.1"}, nil)
	connCtx, connCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer connCancel()
	session, err := client.Connect(connCtx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	// Issue one successful call so we know the session + SSE stream
	// are both fully wired before we trigger shutdown.
	callCtx, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name: mcpserver.ToolSendMessage,
		Arguments: map[string]any{
			"to_agent": "00000000-0000-0000-0000-000000000001",
			"content":  "ping",
		},
	}); err != nil {
		callCancel()
		t.Fatalf("CallTool: %v", err)
	}
	callCancel()

	// Trigger shutdown via Close (the daemon's shutdown path). Time
	// how long it takes. With the Shutdown→Close fallback, the bound
	// is the 5s graceful window plus a small margin for the forced
	// Close + goroutine join.
	closeStart := time.Now()
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- srv.Close()
	}()

	const bound = 7 * time.Second
	select {
	case err := <-closeDone:
		dur := time.Since(closeStart)
		if err != nil {
			t.Errorf("srv.Close returned error: %v", err)
		}
		// Anything under the bound is acceptable. The forced-close
		// path will fire at the 5s mark; the immediate-graceful path
		// completes faster.
		if dur >= bound {
			t.Errorf("srv.Close took %s; want < %s", dur, bound)
		}
		t.Logf("srv.Close returned in %s", dur)
	case <-time.After(bound):
		t.Fatalf("srv.Close did not return within %s — Shutdown->Close fallback regressed", bound)
	}

	// Best-effort session close (it'll fail since the server's gone).
	_ = session.Close()
	runCancel()
	<-runDone
}

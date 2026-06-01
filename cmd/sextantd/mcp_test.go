package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/mcpserver"
	"github.com/love-lena/sextant/pkg/sextantd"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestDaemonMCPServerExposesSendMessage is the end-to-end M10 daemon
// acceptance test: build sextantd, start it, mint a JWT against the
// daemon's own CA, call send_message via the streamable HTTP transport,
// assert the message lands on the inbox subject via the operator NATS
// connection.
func TestDaemonMCPServerExposesSendMessage(t *testing.T) {
	h := startDaemonHarness(t)

	// runtime.json's MCP fields are written after the MCP server binds
	// — that may happen a hair after the control-socket greeting that
	// startDaemonHarness waits for. Poll briefly.
	rt := waitForMCPRuntime(t, h)
	mcpURL := "http://" + rt.MCPHTTPAddr + "/mcp"

	// Issue a JWT against the daemon's CA.
	ca, err := authjwt.LoadCA(h.cfg.CA.KeyPath, h.cfg.CA.PubPath)
	if err != nil {
		t.Fatalf("LoadCA: %v", err)
	}
	now := time.Now()
	token, err := ca.Issue(authjwt.Claims{
		AgentUUID:     uuid.New(),
		IncarnationID: uuid.New(),
		Capabilities:  []string{mcpserver.CapSendMessage},
		IssuedAt:      now,
		ExpiresAt:     now.Add(5 * time.Minute),
		Issuer:        "test",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Subscribe to the destination inbox via the DAEMON principal — since
	// feat-ctl-f0 the broker-scoped operator credential may not core-
	// subscribe agents.*.inbox (the front door). The daemon credential is
	// threaded through runtime.json; the test is a daemon-side witness, so
	// using it is correct.
	if rt.NATSDaemonUser == "" {
		t.Fatal("runtime.json missing nats_daemon_user; daemon did not write the F0 credential")
	}
	nc, err := nats.Connect("nats://"+rt.NATSAddr, nats.UserInfo(rt.NATSDaemonUser, rt.NATSDaemonPassword))
	if err != nil {
		t.Fatalf("nats.Connect: %v", err)
	}
	defer nc.Close()
	toAgent := uuid.New()
	inbox := "agents." + toAgent.String() + ".inbox"
	msgs := make(chan *nats.Msg, 1)
	sub, err := nc.ChanSubscribe(inbox, msgs)
	if err != nil {
		t.Fatalf("subscribe %s: %v", inbox, err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	// Open an MCP client against the daemon.
	transport := &mcp.StreamableClientTransport{
		Endpoint: mcpURL,
		HTTPClient: &http.Client{
			Transport: &daemonBearer{token: token, base: http.DefaultTransport},
			Timeout:   20 * time.Second,
		},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "daemon-test", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	defer session.Close() //nolint:errcheck

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: mcpserver.ToolSendMessage,
		Arguments: map[string]any{
			"to_agent": toAgent.String(),
			"content":  "daemon-e2e",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v\n--- daemon log ---\n%s", err, h.tail(t))
	}
	if res.IsError {
		t.Fatalf("send_message returned error; daemon log:\n%s", h.tail(t))
	}

	select {
	case msg := <-msgs:
		var env sextantproto.Envelope
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload["content"] != "daemon-e2e" {
			t.Errorf("content = %v, want daemon-e2e", payload["content"])
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no inbox message within 3s; daemon log:\n%s", h.tail(t))
	}

	// Sanity: the stdio socket file should exist.
	if rt.MCPStdioSocket == "" || filepath.IsAbs(rt.MCPStdioSocket) != true {
		t.Errorf("runtime.json mcp_stdio_socket missing or non-absolute: %q", rt.MCPStdioSocket)
	}
}

// waitForMCPRuntime polls runtime.json until mcp_http_addr is
// populated (the daemon writes it after binding the MCP listener,
// which can be a hair after the control-socket greeting).
func waitForMCPRuntime(t *testing.T, h *daemonHarness) sextantd.RuntimeInfo {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rt, err := sextantd.ReadRuntimeInfo(h.cfg.Paths.RuntimeFile)
		if err == nil && rt.MCPHTTPAddr != "" {
			return rt
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("runtime.json mcp_http_addr never populated; daemon log:\n%s", h.tail(t))
	return sextantd.RuntimeInfo{}
}

// daemonBearer attaches a static Bearer token to outbound requests.
type daemonBearer struct {
	token string
	base  http.RoundTripper
}

func (d *daemonBearer) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+d.token)
	return d.base.RoundTrip(clone)
}

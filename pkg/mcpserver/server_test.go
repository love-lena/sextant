package mcpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/authjwt"
	"github.com/love-lena/sextant/pkg/mcpserver"
	"github.com/love-lena/sextant/pkg/natsboot"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// ----- Test fixtures -------------------------------------------------------

func requireNATSBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("nats-server")
	if err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}
	return p
}

// bootedNATS brings up a real nats-server with the sextant JetStream
// layout applied. Tests use it to assert real publish round-trips.
func bootedNATS(t *testing.T) *natsboot.Server {
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

// freshCA generates an in-memory CA keypair and returns a *CA bound to
// it. PEM bytes are persisted under t.TempDir to exercise the full
// LoadCA path.
func freshCA(t *testing.T) *authjwt.CA {
	t.Helper()
	priv, pub, err := authjwt.GenerateCA()
	if err != nil {
		t.Fatalf("authjwt.GenerateCA: %v", err)
	}
	dir := t.TempDir()
	privPath := filepath.Join(dir, "ca.key")
	pubPath := filepath.Join(dir, "ca.pub")
	if err := os.WriteFile(privPath, priv, 0o600); err != nil {
		t.Fatalf("write ca.key: %v", err)
	}
	if err := os.WriteFile(pubPath, pub, 0o600); err != nil {
		t.Fatalf("write ca.pub: %v", err)
	}
	ca, err := authjwt.LoadCA(privPath, pubPath)
	if err != nil {
		t.Fatalf("authjwt.LoadCA: %v", err)
	}
	return ca
}

// startServer brings up an mcpserver.Server against the given NATS +
// CA. Returns the server and the operator nats.Conn callers can use to
// subscribe to bus subjects. HTTPPort=0 picks a free port. The stdio
// socket is set under t.TempDir so each test gets its own.
func startServer(t *testing.T, natsSrv *natsboot.Server, ca *authjwt.CA) (*mcpserver.Server, *nats.Conn) {
	t.Helper()
	nc, err := natsSrv.Connect()
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(func() { nc.Close() })

	// macOS limits Unix socket paths to ~104 bytes; t.TempDir under
	// long /var/folders/.../TestName paths can blow past that. Use a
	// short path under os.TempDir() with a random suffix, and clean it
	// up via t.Cleanup.
	sockPath := shortSocketPath(t)
	srv, err := mcpserver.New(mcpserver.Config{
		NATS:        nc,
		CA:          ca,
		From:        sextantproto.Address{Kind: sextantproto.AddressDaemon, ID: "test-daemon"},
		HTTPHost:    "127.0.0.1",
		HTTPPort:    0,
		StdioSocket: sockPath,
	})
	if err != nil {
		t.Fatalf("mcpserver.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = srv.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		_ = srv.Close()
		<-runDone
	})

	// Wait for the HTTP listener to bind before any client tries to
	// connect. 10s is well above what Start should ever take but
	// generous enough that a slow CI box doesn't flake.
	select {
	case <-srv.Ready():
	case <-time.After(10 * time.Second):
		t.Fatalf("mcpserver: not ready within 10s")
	}
	return srv, nc
}

// issueJWT mints a token for a fresh agent UUID with the given caps and
// a 5-minute lifetime.
func issueJWT(t *testing.T, ca *authjwt.CA, caps []string) (string, uuid.UUID) {
	t.Helper()
	agentID := uuid.New()
	now := time.Now()
	token, err := ca.Issue(authjwt.Claims{
		AgentUUID:     agentID,
		IncarnationID: uuid.New(),
		Capabilities:  caps,
		IssuedAt:      now,
		ExpiresAt:     now.Add(5 * time.Minute),
		Issuer:        "test",
	})
	if err != nil {
		t.Fatalf("ca.Issue: %v", err)
	}
	return token, agentID
}

// bearerRoundTripper attaches a static Authorization: Bearer header to
// every outbound HTTP request. The MCP SDK's HTTPClient field accepts
// any *http.Client, so wrapping the default transport keeps the test
// independent of OAuth flows.
type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b *bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(clone)
}

// httpMCPClient connects an MCP client to the server using a Bearer
// token via a custom HTTP transport. Returns a ready-to-use session.
func httpMCPClient(t *testing.T, srv *mcpserver.Server, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint: srv.HTTPURL(),
		HTTPClient: &http.Client{
			Transport: &bearerRoundTripper{token: token, base: http.DefaultTransport},
			Timeout:   20 * time.Second,
		},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// ----- Tests ---------------------------------------------------------------

// TestSendMessageRoundTrip is the M10 acceptance test: a JWT-bearing
// HTTP client invokes send_message and the message lands on the
// destination agent's inbox subject.
func TestSendMessageRoundTrip(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, nc := startServer(t, natsSrv, ca)

	token, _ := issueJWT(t, ca, []string{mcpserver.CapSendMessage})
	toAgent := uuid.New()
	inbox := "agents." + toAgent.String() + ".inbox"

	// Subscribe via the operator NATS conn before issuing the tool call.
	msgs := make(chan *nats.Msg, 1)
	sub, err := nc.ChanSubscribe(inbox, msgs)
	if err != nil {
		t.Fatalf("subscribe %s: %v", inbox, err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	session := httpMCPClient(t, srv, token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: mcpserver.ToolSendMessage,
		Arguments: map[string]any{
			"to_agent": toAgent.String(),
			"content":  "hello",
		},
	})
	if err != nil {
		t.Fatalf("CallTool send_message: %v", err)
	}
	if res.IsError {
		t.Fatalf("send_message returned error: %s", contentText(res))
	}

	select {
	case msg := <-msgs:
		var env sextantproto.Envelope
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			t.Fatalf("unmarshal envelope on %s: %v", inbox, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if got, want := payload["content"], "hello"; got != want {
			t.Errorf("payload content = %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no message on %s within 2s", inbox)
	}
}

// TestSendMessageCapabilityDenied is the M10 acceptance for the
// negative path: a JWT without send_message returns
// {code: capability_denied, details.capability_required: send_message}.
func TestSendMessageCapabilityDenied(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, _ := startServer(t, natsSrv, ca)

	token, _ := issueJWT(t, ca, nil) // no caps

	session := httpMCPClient(t, srv, token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: mcpserver.ToolSendMessage,
		Arguments: map[string]any{
			"to_agent": uuid.NewString(),
			"content":  "should not arrive",
		},
	})
	if err != nil {
		t.Fatalf("CallTool send_message: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error; got success result")
	}
	body := parseToolError(t, res)
	if body["code"] != sextantproto.ErrCodeCapabilityDenied {
		t.Errorf("error code = %v, want %s", body["code"], sextantproto.ErrCodeCapabilityDenied)
	}
	details, _ := body["details"].(map[string]any)
	if details == nil {
		t.Fatalf("expected details on capability_denied; got nil")
	}
	if details["capability_required"] != mcpserver.CapSendMessage {
		t.Errorf("details.capability_required = %v, want %s",
			details["capability_required"], mcpserver.CapSendMessage)
	}
}

// TestUnauthenticatedRejected confirms the HTTP transport rejects
// requests with no Authorization header — defense-in-depth: the SDK
// auth middleware handles this before any tool dispatch runs.
func TestUnauthenticatedRejected(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, _ := startServer(t, natsSrv, ca)

	httpClient := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.HTTPURL(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// TestInvalidJWTRejected confirms a forged token (signed by a
// different CA) is rejected at the auth middleware.
func TestInvalidJWTRejected(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, _ := startServer(t, natsSrv, ca)

	// Mint a token with a different CA.
	otherCA := freshCA(t)
	token, _ := issueJWT(t, otherCA, []string{mcpserver.CapSendMessage})

	transport := &mcp.StreamableClientTransport{
		Endpoint: srv.HTTPURL(),
		HTTPClient: &http.Client{
			Transport: &bearerRoundTripper{token: token, base: http.DefaultTransport},
			Timeout:   5 * time.Second,
		},
		DisableStandaloneSSE: true,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := client.Connect(ctx, transport, nil)
	if err == nil {
		t.Fatalf("expected connect failure on forged token")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unauthor") &&
		!strings.Contains(err.Error(), "401") {
		t.Errorf("connect error should mention auth failure; got: %v", err)
	}
}

// TestListToolsAdvertisesCatalog confirms tools/list returns the M10
// catalog. Smoke test for the registration path; we don't pin the
// exact wire shape.
func TestListToolsAdvertisesCatalog(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, _ := startServer(t, natsSrv, ca)
	token, _ := issueJWT(t, ca, []string{mcpserver.CapSendMessage})
	session := httpMCPClient(t, srv, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := map[string]bool{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		got[tool.Name] = true
	}
	for _, want := range mcpserver.AllTools() {
		if !got[want] {
			t.Errorf("tool %q missing from list", want)
		}
	}
}

// TestGetMetricToolReturnsNotImplemented covers the M10-stub
// fallthrough path that's still in place for tools whose body lands
// later (get_metric ships post-M11 when on-demand telemetry lands).
func TestGetMetricToolReturnsNotImplemented(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, _ := startServer(t, natsSrv, ca)
	token, _ := issueJWT(t, ca, []string{mcpserver.CapReadMetrics})
	session := httpMCPClient(t, srv, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: mcpserver.ToolGetMetric,
		Arguments: map[string]any{
			"name": "agents.active",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result; got success")
	}
	body := parseToolError(t, res)
	if body["code"] != sextantproto.ErrCodeNotImplemented {
		t.Errorf("error code = %v, want %s", body["code"], sextantproto.ErrCodeNotImplemented)
	}
}

// TestSpawnAgentToolWithoutBackendErrors is the M11 surface assertion:
// the tool is wired to the real spawn handler, but if the server was
// built without SpawnDeps the dispatcher surfaces a clean internal
// error rather than crashing. The daemon always populates SpawnDeps;
// the test holds tests that build a bare server accountable.
func TestSpawnAgentToolWithoutBackendErrors(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, _ := startServer(t, natsSrv, ca)
	token, _ := issueJWT(t, ca, []string{mcpserver.CapControlSpawn})
	session := httpMCPClient(t, srv, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: mcpserver.ToolSpawnAgent,
		Arguments: map[string]any{
			"name":     "tester",
			"template": "default",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error result; got success")
	}
	body := parseToolError(t, res)
	if body["code"] != sextantproto.ErrCodeInternal {
		t.Errorf("error code = %v, want %s", body["code"], sextantproto.ErrCodeInternal)
	}
}

// TestAuditEnvelopeOnSendMessage exercises the audit publish path: a
// successful tool call writes one audit.tool_call envelope.
func TestAuditEnvelopeOnSendMessage(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, nc := startServer(t, natsSrv, ca)

	auditCh := make(chan *nats.Msg, 1)
	sub, err := nc.ChanSubscribe("audit.tool_call", auditCh)
	if err != nil {
		t.Fatalf("subscribe audit.tool_call: %v", err)
	}
	defer sub.Unsubscribe() //nolint:errcheck

	token, agentID := issueJWT(t, ca, []string{mcpserver.CapSendMessage})
	session := httpMCPClient(t, srv, token)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: mcpserver.ToolSendMessage,
		Arguments: map[string]any{
			"to_agent": uuid.NewString(),
			"content":  "hi",
		},
	}); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	select {
	case msg := <-auditCh:
		var env sextantproto.Envelope
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			t.Fatalf("unmarshal audit envelope: %v", err)
		}
		var payload sextantproto.AuditPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Fatalf("unmarshal audit payload: %v", err)
		}
		if payload.Action != "tool_call.send_message" {
			t.Errorf("audit action = %q, want tool_call.send_message", payload.Action)
		}
		if payload.Result != sextantproto.AuditAllowed {
			t.Errorf("audit result = %q, want %q", payload.Result, sextantproto.AuditAllowed)
		}
		if payload.Actor != agentID.String() {
			t.Errorf("audit actor = %q, want %s", payload.Actor, agentID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no audit.tool_call envelope within 2s")
	}
}

// ----- helpers -------------------------------------------------------------

// shortSocketPath returns a Unix socket path short enough to fit under
// macOS' 104-byte sun_path cap. t.TempDir on macOS sits under
// /var/folders/<long>/T/<test-name>/<seq> which routinely exceeds the
// limit. The replacement path lives under /tmp/sxt-mcp-<rand>.sock and
// is removed on test cleanup.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	// Use a 12-char random suffix so concurrent tests don't collide.
	id := uuid.New().String()[:12]
	p := filepath.Join("/tmp", "sxt-mcp-"+id+".sock")
	t.Cleanup(func() { _ = os.Remove(p) })
	return p
}

func contentText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// parseToolError unmarshals the JSON-encoded tool error body the
// dispatcher emits into a map. The dispatcher serializes
// {code, message, details} per server.go's toolErrorResult.
func parseToolError(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatalf("expected error content; got none")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent; got %T", res.Content[0])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &body); err != nil {
		t.Fatalf("unmarshal tool error body %q: %v", tc.Text, err)
	}
	return body
}

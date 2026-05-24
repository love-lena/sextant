package mcpserver_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/love-lena/sextant-initial/pkg/mcpserver"
)

// unixSocketTransport is a Transport that opens a fresh unix socket
// connection on Connect. Each Connect call returns a separate session
// (the MCP server's accept loop spawns one ServerSession per accepted
// conn).
type unixSocketTransport struct {
	path string
}

func (u *unixSocketTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", u.path)
	if err != nil {
		return nil, err
	}
	// IOTransport.Connect wraps the rwc — we mimic its setup using the
	// public API by delegating to mcp.IOTransport. Note: IOTransport
	// uses Reader+Writer pair, so we pass the same conn to both.
	delegate := &mcp.IOTransport{
		Reader: conn,
		Writer: writeCloserOnly{conn},
	}
	return delegate.Connect(ctx)
}

// writeCloserOnly hides the Read method of a net.Conn so the SDK's
// IOTransport treats it strictly as the write half. Closing it closes
// the underlying conn — that's the test's intended behavior.
type writeCloserOnly struct {
	net.Conn
}

func (w writeCloserOnly) Read(_ []byte) (int, error) { return 0, nil }

// TestStdioOperatorBypassesCapCheck confirms that local stdio callers
// (no JWT) inherit operator authority — capability checks are skipped
// per architecture.md §10b.
func TestStdioOperatorBypassesCapCheck(t *testing.T) {
	natsSrv := bootedNATS(t)
	ca := freshCA(t)
	srv, _ := startServer(t, natsSrv, ca)

	// Operator callers have no JWT and no caps; the dispatcher must
	// still allow send_message.
	sockPath := srv.StdioSocketPath()
	if sockPath == "" {
		t.Fatalf("stdio socket not configured")
	}

	// The server uses an Listen+Accept model; we dial it as the client.
	transport := &unixSocketTransport{path: sockPath}
	client := mcp.NewClient(&mcp.Implementation{Name: "operator-test", Version: "0.0.1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect via unix socket: %v", err)
	}
	defer session.Close() //nolint:errcheck

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: mcpserver.ToolSendMessage,
		Arguments: map[string]any{
			"to_agent": uuid.NewString(),
			"content":  "operator hello",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("operator path returned tool error: %s", contentText(res))
	}
}

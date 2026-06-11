// Command sextant-mcp makes a Claude Code session a first-class sextant
// client (ADR-0028). It is a stdio MCP server holding one bus connection
// under one verified identity for the whole session: the one-shot and
// pull-batch verbs (ADR-0017) are MCP tools, and push-stream delivery rides
// the Claude Code channel mechanism — inbound frames on subscribed subjects
// are pushed into the session as <channel> events. The reply path is the
// message_publish tool.
//
// Identity is the server's own, never the operator's (ADR-0029): explicit
// --creds/$SEXTANT_CREDS or --context/$SEXTANT_CONTEXT win, but with nothing
// pinned the server provisions a dedicated per-session identity rather than
// inheriting the operator's active context. Resolution is lazy and retried per
// tool call, so the handshake always succeeds and the server heals once a bus
// is reachable.
//
//	sextant-mcp                       # mint/reattach this session's own identity
//	sextant-mcp --context my-agent    # a saved context (or $SEXTANT_CONTEXT)
//	sextant-mcp --creds path/to.creds --store path/to/bus-store
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/love-lena/sextant/internal/version"
	"github.com/love-lena/sextant/pkg/sextant"
)

const serverName = "sextant"

const instructions = "Tools for collaborating over the sextant bus. " +
	"Use message_read to catch up on a subject, message_subscribe to follow it live " +
	"(frames arrive as channel events; reply with message_publish), and the artifact " +
	"tools for shared state. The bundled sextant skill documents conventions, record " +
	"shapes, and identity setup."

func main() {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	fs := flag.NewFlagSet("sextant-mcp", flag.ExitOnError)
	cf := addConnFlags(fs)
	ver := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *ver {
		fmt.Println("sextant-mcp " + version.String())
		return
	}

	if err := run(context.Background(), cf); err != nil {
		log.Fatalf("sextant-mcp: %v", err)
	}
}

func run(ctx context.Context, cf connFlags) error {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: version.String()},
		&mcp.ServerOptions{
			Instructions: instructions,
			Capabilities: &mcp.ServerCapabilities{
				// The channel declaration: Claude Code injects
				// notifications/claude/channel events from this server
				// into the session (research preview, allowlist-gated).
				Experimental: map[string]any{"claude/channel": map[string]any{}},
			},
		},
	)

	conn := &connManager{cf: cf}
	names := newNameCache(func(ctx context.Context) ([]sextant.ClientInfo, error) {
		c, err := conn.get(ctx)
		if err != nil {
			return nil, err
		}
		return c.ListClients(ctx)
	})
	transport := &capturingTransport{inner: &mcp.StdioTransport{}}
	registerTools(server, &deps{
		conn:  conn,
		names: names,
		hub:   newChannelHub(transport.notify, names),
	})

	return server.Run(ctx, transport)
}

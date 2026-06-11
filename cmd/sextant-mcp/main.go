// Command sextant-mcp makes a Claude Code session a first-class sextant
// client (ADR-0028). It is a stdio MCP server holding one bus connection
// under one verified identity for the whole session: the one-shot and
// pull-batch verbs (ADR-0017) are MCP tools, and push-stream delivery rides
// the Claude Code channel mechanism — inbound frames on subscribed subjects
// are pushed into the session as <channel> events. The reply path is the
// message_publish tool.
//
// Identity resolves like the operator CLI (--creds/$SEXTANT_CREDS →
// --context/$SEXTANT_CONTEXT → the active context) but lazily: the MCP
// handshake always succeeds, and tool calls retry resolution + connection
// until one works, so `sextant clients register --self` run mid-session
// heals a fresh machine without a restart.
//
//	sextant-mcp                       # resolve the active context
//	sextant-mcp --context my-agent    # a saved context (or $SEXTANT_CONTEXT)
//	sextant-mcp --creds path/to.creds --store path/to/bus-store
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if err := run(context.Background(), cf); err != nil {
		log.Fatalf("sextant-mcp: %v", err)
	}
}

func run(ctx context.Context, cf connFlags) error {
	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
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

	return server.Run(ctx, &mcp.StdioTransport{})
}

const serverVersion = "0.1.0"

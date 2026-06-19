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
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/love-lena/sextant/clients/go/apps/internal/version"
	"github.com/love-lena/sextant/clients/go/sdk"
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

	// The `attest` subcommand is the claude-code plugin's UserPromptSubmit hook
	// body (ADR-0030, TASK-56) — same binary, so it reuses the per-session
	// identity/context resolution. Dispatch it before the server flag parse.
	if len(os.Args) > 1 && os.Args[1] == "attest" {
		os.Exit(runAttest(os.Args[2:]))
	}

	// The `status` subcommand is the plugin's PostToolUse status hook (TASK-87) —
	// same binary, reusing the per-session identity gating the attest hook uses.
	if len(os.Args) > 1 && os.Args[1] == "status" {
		os.Exit(runStatus(os.Args[2:]))
	}

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

	// The durable per-session store (TASK-124): subscriptions + active context
	// survive a resume / compaction / fresh MCP process. Keyed on the stable
	// session id under the writable plugin-data dir; a missing file degrades to
	// empty (nothing to restore). Shared by the connManager (context) and the
	// channel hub (subjects + seq cursors).
	state := loadSubstate(os.Getenv("CLAUDE_PLUGIN_DATA"), os.Getenv(sessionEnv))

	conn := &connManager{
		cf:   cf,
		base: ctx,
		// Record the connected identity per session so the attest hook follows
		// it (ADR-0029/0030). Both come from the same env Claude Code sets on the
		// hook process, so the hook reads the file this server writes.
		pluginData: os.Getenv("CLAUDE_PLUGIN_DATA"),
		sessionID:  os.Getenv(sessionEnv),
		state:      state,
	}
	// Re-pin a context_use'd identity across a resume BEFORE resolve()'s auto-mint
	// fallback (TASK-124, mode C): a fresh process lost connManager.switched, so
	// without this it would reconnect as the auto-mint id, not the context the
	// agent switched to. Validated against the agent-kind guard (ADR-0029) so a
	// since-deleted/recreated non-agent context is not assumed; an explicit
	// --creds/--context still overrides it (resolve checks those first).
	conn.restorePersistedContext()
	names := newNameCache(func(ctx context.Context) ([]sextant.ClientInfo, error) {
		c, err := conn.get(ctx)
		if err != nil {
			return nil, err
		}
		return c.ListClients(ctx)
	})
	transport := &capturingTransport{inner: &mcp.StdioTransport{}}
	hub := newChannelHub(transport.notify, names)
	hub.state = state // share the durable store the connManager holds (TASK-124)
	// On every connect: bridge the SDK auto-DM channel into the channel-wake path
	// (ADR-0030, M1) AND restore the persisted manual subscriptions, catching each
	// up from its last delivered seq (TASK-124). Connect is lazy (first tool call),
	// so both begin after the worker's first sextant interaction — acceptable for
	// v1; eager connect-at-startup would fail before the bus is up.
	conn.onConnect = func(c *sextant.Client) {
		hub.startInboxDrain(c)
		hub.restoreSubs(c)
	}
	conn.onDiscard = hub.discardClient
	registerTools(server, &deps{
		conn:  conn,
		names: names,
		hub:   hub,
	})

	// Persist pending seq advances periodically — advance() only marks the state
	// dirty to avoid a disk write per delivered frame — and once more on shutdown.
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				state.flush()
			}
		}
	}()

	err := server.Run(ctx, transport)
	state.flush()
	return err
}

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/love-lena/sextant/internal/attest"
	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
)

// attest is the claude-code plugin's UserPromptSubmit hook body (ADR-0030,
// TASK-56). The plugin's hooks.json invokes `sextant-mcp attest` — the SAME
// installed binary as the MCP server, so it reuses the MCP server's per-session
// identity/context resolution (clictx, ADR-0029): it connects as the same worker
// identity, sees the same bus, and scans the worker's own always-on DM subject
// (msg.client.<self>, the auto-subscribe from TASK-55).
//
// It reads NEW inbound frames since a persisted per-session cursor, stamps each by
// its unforgeable bus-stamped author ULID with a trust level (attest.Classify),
// and emits one trusted additionalContext block (hookSpecificOutput) that the
// harness injects UNWRAPPED — so a validated message never reaches the agent under
// the untrusted wrapper (AC#5). The cursor (keyed on CLAUDE_CODE_SESSION_ID, under
// the writable CLAUDE_PLUGIN_DATA) makes delivery at-most-once and resume-durable
// (AC#6).
//
// Discipline (fail-loud / fail-early): the whole bus read is bounded well under
// the hard, non-configurable 30s UserPromptSubmit timeout. On ANY error or
// timeout it degrades to exit 0 with NO additionalContext — it never blocks or
// hangs the turn. Diagnostics go to stderr.
//
// TASK-57 coordination: at this slice the channel still PUSHES content (wrapped);
// this hook delivers the trusted copy and the agent is told to trust this copy
// over any wrapped one. TASK-57 turns the channel into a wake-only notification so
// no wrapped copy is delivered at all; only the channel push side changes — this
// hook is already the sole content path.

// hookInput is the UserPromptSubmit hook stdin contract (claude-code).
type hookInput struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	Prompt        string `json:"prompt"`
}

// attestBudget bounds the bus work well under the hard 30s hook timeout.
const attestBudget = 5 * time.Second

// runAttest is the hook entrypoint. It NEVER returns a non-zero exit for a bus
// problem — degrade-to-silent is the contract. args is os.Args[2:] (after the
// "attest" subcommand) so the hook can still pass --context/--store/--creds if a
// deployment needs them; normally it relies on $SEXTANT_CONTEXT / the active
// context, exactly as the MCP server does.
func runAttest(args []string) int {
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	fs := flag.NewFlagSet("sextant-mcp attest", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cf := addConnFlags(fs)
	if err := fs.Parse(args); err != nil {
		log.Printf("sextant-mcp attest: flags: %v", err)
		return 0 // never block the turn on a flag slip
	}

	// Read the hook stdin (session id is the cursor key). A missing/garbled stdin
	// is survivable — fall back to no session id.
	var in hookInput
	if b, err := io.ReadAll(os.Stdin); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &in); err != nil {
			log.Printf("sextant-mcp attest: undecodable hook stdin, continuing: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), attestBudget)
	defer cancel()

	out, err := attestOnce(ctx, cf, in.SessionID)
	if err != nil {
		log.Printf("sextant-mcp attest: %v (degrading: no context injected)", err)
		return 0
	}
	if out != "" {
		fmt.Println(out)
	}
	return 0
}

// attestOnce does the bus work and returns the hook stdout JSON (or "" when there
// is nothing new). Every step that can hang is governed by ctx.
func attestOnce(ctx context.Context, cf connFlags, sessionID string) (string, error) {
	rc, err := clictx.Resolve(*cf.creds, *cf.url, *cf.context)
	if err != nil {
		return "", fmt.Errorf("resolve identity: %w", err)
	}
	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    rc.Creds,
		URL:          rc.URL,
		ConnInfoPath: cf.connInfoPath(),
		Logf:         log.Printf,
	})
	if err != nil {
		return "", fmt.Errorf("connect: %w", err)
	}
	defer c.Close()

	self := c.ID()
	subject := sx.ClientSubject(self) // the worker's always-on DM (TASK-55)

	// The principal designation (TASK-54): discovered on connect, the unforgeable
	// basis for operator-equivalence. Prefer the cached value; fall back to an
	// explicit read if the hello handshake hasn't populated it.
	principal := c.Principal()
	if principal == "" {
		if p, err := c.GetPrincipal(ctx); err == nil {
			principal = p
		} else {
			log.Printf("sextant-mcp attest: principal read failed, treating as none: %v", err)
		}
	}

	// The registry: which authors resolve to a registered client (verified peer)
	// vs. unknown. A failure here is non-fatal — without it, only the principal is
	// distinguishable and everyone else falls to UNKNOWN (the safe floor).
	registered := map[string]bool{}
	if clients, err := c.ListClients(ctx); err == nil {
		for _, ci := range clients {
			registered[ci.ID] = true
		}
	} else {
		log.Printf("sextant-mcp attest: client list failed, peers will read as UNKNOWN: %v", err)
	}

	// The cursor: read only what's new since this session last looked.
	dataDir := os.Getenv("CLAUDE_PLUGIN_DATA")
	if dataDir == "" {
		// No writable plugin data dir: we can still stamp, but can't persist a
		// cursor, so we'd re-deliver. Refuse to spam — read nothing new.
		return "", fmt.Errorf("CLAUDE_PLUGIN_DATA unset; cannot persist cursor")
	}
	cur, err := attest.LoadCursor(dataDir, sessionID)
	if err != nil {
		log.Printf("sextant-mcp attest: cursor load: %v (starting clean)", err)
	}

	frames, next, err := c.FetchMessages(ctx, subject, cur.Since(subject), 200)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", subject, err)
	}

	stamped := attest.Stamp(frames, self, principal, registered)
	block := attest.BuildContext(stamped, principal)

	// Advance + persist the cursor only after a successful read. We advance to the
	// batch's next cursor regardless of whether any frame survived self-filtering,
	// so our own echoes don't wedge the cursor.
	cur.Advance(subject, next)
	if err := cur.Save(); err != nil {
		// Persist failure means a possible re-delivery next turn; surface it but
		// still deliver this turn's stamp (the agent seeing it twice beats never).
		log.Printf("sextant-mcp attest: cursor save: %v (may re-deliver next turn)", err)
	}

	if block == "" {
		return "", nil
	}
	b, err := attest.Marshal(block)
	if err != nil {
		return "", fmt.Errorf("marshal hook output: %w", err)
	}
	return string(b), nil
}

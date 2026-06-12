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
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
)

// attest is the claude-code plugin's UserPromptSubmit hook body (ADR-0030,
// TASK-56). The plugin's hooks.json invokes `sextant-mcp attest` — the SAME
// installed binary as the MCP server, so it reuses the MCP server's per-session
// identity resolution (connManager.resolve, ADR-0029): it connects as the same
// worker identity, sees the same bus, and scans the worker's own always-on DM
// subject (msg.client.<self>, the auto-subscribe from TASK-55).
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
// deployment needs them; normally it relies on this session's own per-session
// identity (claude-<session-id>), exactly as the MCP server does (ADR-0029).
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

	// emit writes the trusted block to stdout. attestOnce calls this BEFORE
	// advancing the cursor (M2): the cursor only moves once the
	// operator-equivalent block is on its way to the harness. A write failure
	// leaves the cursor untouched, so the next turn re-delivers — re-delivery
	// beats a silent loss on the trust path. os.Stdout is an *os.File whose
	// Write goes straight to the write(2) syscall (no userspace buffer), so a
	// successful Fprintln is the durable hand-off; we do NOT fsync — the hook's
	// stdout is a pipe to the harness, never a regular file, and Sync on a pipe
	// fails with EBADF/EINVAL even though the bytes are already delivered.
	emit := func(out string) error {
		_, err := fmt.Fprintln(os.Stdout, out)
		return err
	}

	if err := attestOnce(ctx, cf, in.SessionID, emit); err != nil {
		log.Printf("sextant-mcp attest: %v (degrading: no context injected)", err)
		return 0
	}
	return 0
}

// attestOnce does the bus work, emits the trusted hook block via emit (when there
// is something new), and only THEN advances and persists the delivery cursor.
// Every step that can hang is governed by ctx. emit must write the block to the
// hook's stdout and confirm it is flushed; attestOnce treats an emit error as a
// non-delivery and leaves the cursor where it was so the block re-delivers next
// turn (M2: advance-after-emit, never before).
//
// Delivery guarantee: this is AT-MOST-ONCE on a SUCCESSFUL emit — once emit
// returns nil, the cursor advances and the block is never re-delivered by this
// hook. There is one residual window outside our control: the harness itself may
// discard an injected additionalContext block after we have flushed it (we cannot
// observe that from here, and we do NOT advance again to recover it). The recovery
// path for that window is independent: a Monitor (`sextant subscribe
// msg.client.<self>`) tails the same DM subject live, so a block the harness drops
// is still visible on the bus. Full deliver-then-confirm (a two-phase ack from the
// harness) is deferred — not over-engineered for v1.
func attestOnce(ctx context.Context, cf connFlags, sessionID string, emit func(string) error) error {
	// Resolve the SAME per-session identity the MCP server connects as. attest is
	// a separate process from the server, but the plugin spawns both with the same
	// env (CLAUDE_CODE_SESSION_ID, $SEXTANT_HOME/--store, any pinned creds/context),
	// so routing through the server's own connManager.resolve gives byte-identical
	// precedence (ADR-0029): (1) pinned $SEXTANT_CREDS/$SEXTANT_CONTEXT, (2) an
	// in-session context_use switch — never observable here, so it no-ops, (3)
	// this session's own identity, reattached by the claude-<session-id> handle via
	// clictx.Load. The handle and the on-disk context store are shared, so whichever
	// process minted it first, both reattach to it — attest and the server stay in
	// lockstep by construction and attest scans the worker's OWN DM subject, never a
	// stranger's. Using resolve (not plain clictx.Resolve) is what closes the
	// forward-risk the pre-#107 branch flagged here.
	rm := &connManager{cf: cf}
	rc, err := rm.resolve(ctx)
	if err != nil {
		return fmt.Errorf("resolve identity: %w", err)
	}
	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    rc.Creds,
		URL:          rc.URL,
		ConnInfoPath: cf.connInfoPath(),
		Logf:         log.Printf,
	})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
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
		return fmt.Errorf("CLAUDE_PLUGIN_DATA unset; cannot persist cursor")
	}
	cur, err := attest.LoadCursor(dataDir, sessionID)
	if err != nil {
		log.Printf("sextant-mcp attest: cursor load: %v (starting clean)", err)
	}

	frames, next, err := c.FetchMessages(ctx, subject, cur.Since(subject), 200)
	if err != nil {
		return fmt.Errorf("read %s: %w", subject, err)
	}

	stamped := attest.Stamp(frames, self, principal, registered)
	block := attest.BuildContext(stamped, principal)

	// advanceCursor moves the cursor to the batch's next sequence and persists it.
	// We advance to next regardless of whether any frame survived self-filtering,
	// so our own echoes don't wedge the cursor. It runs only AFTER a successful
	// emit (or when there is nothing to emit) — never before (M2).
	advanceCursor := func() {
		cur.Advance(subject, next)
		if err := cur.Save(); err != nil {
			// Persist failure means a possible re-delivery next turn; surface it.
			// The block is already out, so the agent seeing it twice beats never.
			log.Printf("sextant-mcp attest: cursor save: %v (may re-deliver next turn)", err)
		}
	}

	// Nothing new (or only self-echoes): advance past them and return — there is
	// no block to emit, so re-delivery is not a concern.
	if block == "" {
		advanceCursor()
		return nil
	}

	b, err := attest.Marshal(block)
	if err != nil {
		// Can't form the block: do NOT advance — re-read next turn.
		return fmt.Errorf("marshal hook output: %w", err)
	}

	// Emit FIRST. Only once the block is written to stdout do we advance the
	// cursor (M2). If emit fails, leave the cursor where it was so this batch
	// re-delivers next turn — re-delivery beats a silent at-most-once loss on the
	// operator-equivalent trust path.
	if err := emit(string(b)); err != nil {
		return fmt.Errorf("emit hook output (cursor not advanced, will re-deliver): %w", err)
	}
	advanceCursor()
	return nil
}

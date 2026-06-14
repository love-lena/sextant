package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/love-lena/sextant/internal/attest"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
)

// fetchFunc is the cursor-paged read attestOnce depends on — c.FetchMessages in
// production, a fake in tests. Pulling it out lets gatherInbound's fail-soft
// branch be exercised without a live bus.
type fetchFunc func(ctx context.Context, subject string, since uint64, limit int) ([]wire.Frame, uint64, error)

// subjectAdvance is a (subject, next-cursor) pair queued for advancing — recorded
// only for subjects that fetched cleanly, so a skipped subject is never marked
// delivered.
type subjectAdvance struct {
	subject string
	next    uint64
}

// gatherInbound fetches each subject from its own cursor and stamps the frames
// into one batch, with the SAME content-blind classifier for every subject. The
// cursor is per-subject, so the subjects advance independently. Fail-soft per
// subject: a fetch error on one must not drop the others — it logs, skips that
// subject this turn, and (by omitting it from the returned advances) leaves its
// cursor put so it re-reads next turn.
func gatherInbound(ctx context.Context, fetch fetchFunc, subjects []string, cur *attest.Cursor, self, principal string, registered map[string]bool) ([]attest.Stamped, []subjectAdvance) {
	var stamped []attest.Stamped
	var advances []subjectAdvance
	for _, subj := range subjects {
		frames, next, err := fetch(ctx, subj, cur.Since(subj), 200)
		if err != nil {
			log.Printf("sextant-mcp attest: read %s: %v (skipping this subject this turn)", subj, err)
			continue
		}
		stamped = append(stamped, attest.Stamp(frames, self, principal, registered)...)
		advances = append(advances, subjectAdvance{subject: subj, next: next})
	}
	return stamped, advances
}

// attest is the claude-code plugin's UserPromptSubmit hook body (ADR-0030,
// TASK-56). The plugin's hooks.json invokes `sextant-mcp attest` — the SAME
// installed binary as the MCP server, but a SEPARATE process. It does NOT
// re-resolve identity (that diverges from the server in the unpinned default
// path — a context_use switch the hook can't observe, a concurrent first-turn
// mint, a session-id source mismatch). Instead it FOLLOWS the server: the server
// records the identity it connected as to a per-session file (attest.Identity,
// keyed on CLAUDE_CODE_SESSION_ID under CLAUDE_PLUGIN_DATA, written on every
// (re)connect), and the hook loads those exact creds and connects co-identity. So
// it scans the SAME worker's always-on inbox (msg.client.<self>, the auto-subscribe
// from TASK-55) the server is on — and, when a principal is designated, the
// worker's 2-party DM topic with the principal (sx.DMSubject(self, principal),
// ADR-0034/TASK-90), so a principal DM is stamped just like an inbox drop. Lockstep
// by construction, in every case (pinned creds/context, a context_use switch, or a
// per-session mint).
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
// "attest" subcommand). --store/--url still tune discovery, but identity is NOT
// re-resolved here: the hook reads the per-session identity file the MCP server
// wrote (the server is the sole identity resolver, ADR-0029) and connects as
// exactly that identity, so it can never diverge from the server.
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
	// The cursor and the identity file share the writable plugin-data dir, keyed
	// on the session id. Without it we can neither follow the server's identity
	// nor persist a cursor — degrade to silent rather than re-resolve or re-deliver.
	dataDir := os.Getenv("CLAUDE_PLUGIN_DATA")
	if dataDir == "" {
		return fmt.Errorf("CLAUDE_PLUGIN_DATA unset; cannot follow the server identity or persist a cursor")
	}

	// FOLLOW the server's identity, never re-resolve it (ADR-0029/0030). The MCP
	// server is the only process that resolves/mints; it records what it connected
	// as to a per-session file, and we connect as exactly that. This is lockstep by
	// construction — pinned creds/context, a context_use switch, and a per-session
	// mint all land on the same identity the server is live on, killing the C1/C2/M2
	// divergence classes the independent-resolve path had. If the file is MISSING the
	// server has not connected yet (e.g. turn 1, before the first tool call): degrade
	// to silent — turn 1 has no inbound messages to attest anyway.
	// Key the identity lookup on the SAME source the server wrote it under —
	// os.Getenv(sessionEnv), exactly the server's m.sessionID — NOT the stdin
	// session_id. This makes the lockstep hold by construction even if the
	// harness ever diverged the stdin session_id from the env one (the M2 trap):
	// both processes resolve the identity file from the identical env value. The
	// cursor below stays keyed on the stdin sessionID (the hook's own per-session
	// delivery tracking, which need not match the server).
	id, err := attest.LoadIdentity(dataDir, os.Getenv(sessionEnv))
	if err != nil {
		if errors.Is(err, attest.ErrNoIdentity) {
			log.Printf("sextant-mcp attest: no session identity yet (server not connected); nothing to attest")
			return nil
		}
		return fmt.Errorf("load session identity: %w", err)
	}
	// Prefer the URL the server resolved; fall back to --url, then to discovery
	// under --store (the same connInfoPath the server uses).
	url := id.URL
	if url == "" {
		url = *cf.url
	}
	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    id.Creds,
		URL:          url,
		ConnInfoPath: cf.connInfoPath(),
		Logf:         log.Printf,
	})
	if err != nil {
		return fmt.Errorf("connect as the server's identity (%s): %w", id.Creds, err)
	}
	defer c.Close()

	self := c.ID()
	inbox := sx.ClientSubject(self) // the worker's always-on inbox (TASK-55)

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

	// The cursor: read only what's new since this session last looked. It keys on
	// the stdin session_id (sessionID) under the same plugin-data dir as the
	// identity file, so identity and delivery resume together.
	cur, err := attest.LoadCursor(dataDir, sessionID)
	if err != nil {
		log.Printf("sextant-mcp attest: cursor load: %v (starting clean)", err)
	}

	// The subjects to scan: always the inbox (the one-way wake floor), plus the
	// principal DM topic when a principal is designated and is not us. A DM is the
	// default for back-and-forth (ADR-0034), so a principal DM on a 2-party topic
	// must be stamped exactly like the inbox — otherwise it is second-class on the
	// trust path, contradicting "DMs as default over inboxes". Both are scanned
	// with the SAME content-blind classifier; only the subject set widens.
	subjects := []string{inbox}
	if principal != "" && principal != self {
		subjects = append(subjects, sx.DMSubject(self, principal))
	}

	// Fetch each subject from its own cursor and stamp into one batch, fail-soft
	// per subject (see gatherInbound).
	stamped, advances := gatherInbound(ctx, c.FetchMessages, subjects, cur, self, principal, registered)

	block := attest.BuildContext(stamped, principal)

	// advanceCursor moves every cleanly-fetched subject's cursor to its batch's next
	// sequence and persists them. We advance regardless of whether any frame survived
	// self-filtering, so our own echoes don't wedge a cursor. It runs only AFTER a
	// successful emit (or when there is nothing to emit) — never before (M2).
	advanceCursor := func() {
		for _, a := range advances {
			cur.Advance(a.subject, a.next)
		}
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

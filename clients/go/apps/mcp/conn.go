package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/love-lena/sextant/clients/go/apps/internal/clictx"
	"github.com/love-lena/sextant/clients/go/apps/internal/selfenroll"
	"github.com/love-lena/sextant/clients/go/apps/mcp/attest"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
)

// agentKind labels the identities the MCP server mints for itself, so the
// clients directory tells an AI agent apart from a human or a worker.
const agentKind = "agent"

// sessionEnv is Claude Code's per-conversation id, set on every spawned stdio
// MCP server and stable across `--resume`/`--continue`. The agent context
// handle is keyed on it so a resumed session reattaches to the identity it
// minted before, instead of coming back as a stranger (ADR-0029).
const sessionEnv = "CLAUDE_CODE_SESSION_ID"

// connFlags mirror the operator CLI's connection flags (cmd/sextant), so the
// MCP server is configured the same way every other client is.
type connFlags struct {
	creds   *string
	store   *string
	url     *string
	context *string
}

func addConnFlags(fs *flag.FlagSet) connFlags {
	return connFlags{
		creds:   fs.String("creds", os.Getenv("SEXTANT_CREDS"), "client credentials file (or set $SEXTANT_CREDS)"),
		store:   fs.String("store", defaultStore(), "bus store directory for discovery (or set $SEXTANT_STORE)"),
		url:     fs.String("url", "", "bus URL (default: discovery file under --store)"),
		context: fs.String("context", os.Getenv("SEXTANT_CONTEXT"), "saved context to connect as (default: the active one)"),
	}
}

// defaultStore mirrors cmd/sextant's default exactly: $SEXTANT_STORE, then
// the user config dir, then a relative fallback.
func defaultStore() string {
	if v := os.Getenv("SEXTANT_STORE"); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "sextant", "jetstream")
	}
	return filepath.Join(".sextant", "jetstream")
}

func (cf connFlags) connInfoPath() string {
	return filepath.Join(*cf.store, conninfo.DefaultFile)
}

// connManager holds the one bus connection for the server's lifetime
// (ADR-0012: one server, one verified identity; presence derives from the
// live connection, ADR-0020). Identity problems defer rather than exit: every
// get re-runs resolution, so once a bus becomes reachable (or Claude switches
// context) the server heals without a restart.
type connManager struct {
	cf connFlags

	// base is the server-lifetime context the held connection is built on. The
	// SDK ties a connection's subscriptions — including the auto-inbox subscription
	// (TASK-55) that feeds the M1 wake bridge — to the context passed to Connect:
	// a cancelled connect ctx tears those subscriptions down. So we MUST connect
	// on a context that outlives any single tool call; using the per-request ctx
	// would kill the auto-inbox sub the instant the first tool returned (it did, in
	// review). This mirrors the explicit message_subscribe path, which likewise
	// subscribes on a non-request context so the subscription outlives the call.
	// nil falls back to context.Background() (unit tests that never connect).
	base context.Context

	// onConnect fires once for each newly-established client; onDiscard fires
	// when a drained client is dropped before a fresh connect. The MCP server
	// wires these to the channel hub's inbox drain (start on connect, stop on
	// discard) so a principal DM wakes the session (ADR-0030, review M1). Both
	// are optional — nil in unit tests that exercise resolution/provenance only.
	onConnect func(*sextant.Client)
	onDiscard func(*sextant.Client)

	// pluginData is the writable CLAUDE_PLUGIN_DATA dir; sessionID is the stable
	// CLAUDE_CODE_SESSION_ID. When both are set, every successful (re)connect
	// records the connected identity to a per-session file (attest.SaveIdentity)
	// so the attest hook — a SEPARATE process — FOLLOWS the server's identity
	// instead of re-resolving it (ADR-0029/0030: the server is the sole identity
	// resolver). Empty in unit tests / non-Claude-Code hosts: the write is then a
	// logged no-op and the hook degrades to silent.
	pluginData string
	sessionID  string

	// state is the durable per-session store (TASK-124). use() records the
	// context_use'd identity here so a fresh process re-pins it (main pre-loads
	// switched from it) instead of reverting to the auto-mint id (mode C). nil in
	// the resolution/provenance unit tests, which never switch contexts.
	state *substate

	mu     sync.Mutex
	client *sextant.Client
	// switched is the context Claude explicitly attached to via context_use;
	// empty means "use this session's own auto-provisioned identity". On a fresh
	// process main pre-loads it from the durable state so the switch survives a
	// resume; resolve() still lets an explicit --creds/--context override it.
	switched string
	// mint provisions a fresh agent identity. nil uses the real bus-backed
	// implementation (mintAgent); tests inject a stub to exercise resolution
	// branching without a bus.
	mint func(ctx context.Context, name, display string) (clictx.ResolvedConn, error)
}

// resolve picks the identity to connect as — like the operator CLI for the
// explicit cases, but it DELIBERATELY never falls back to clictx.Active(): an
// MCP server must never inherit the human operator's identity (ADR-0029).
// Precedence: explicit creds/context (env or flag) → a context Claude switched
// to → this session's own dedicated identity (reattached by CLAUDE_CODE_SESSION_ID,
// else freshly minted).
func (m *connManager) resolve(ctx context.Context) (clictx.ResolvedConn, error) {
	if *m.cf.creds != "" || *m.cf.context != "" {
		return clictx.Resolve(*m.cf.creds, *m.cf.url, *m.cf.context)
	}
	if m.switched != "" {
		return clictx.Resolve("", *m.cf.url, m.switched)
	}
	name, persistent, err := agentContextName()
	if err != nil {
		return clictx.ResolvedConn{}, err
	}
	if persistent {
		if c, err := clictx.Load(name); err == nil {
			return clictx.ResolvedConn{Creds: c.Creds, URL: orStr(*m.cf.url, c.URL), Context: c.Name}, nil
		}
	}
	// Falling through to a fresh per-session auto-mint (ADR-0029's default for an
	// un-configured session). Emit a one-line notice — symmetric to the no-bus
	// error above (it likewise points at $SEXTANT_CONTEXT) — so the auto-mint dance
	// is self-documenting: a named crew agent pins a stable identity by setting
	// $SEXTANT_CONTEXT at launch (TASK-76). Notice only — the default behaviour is
	// unchanged.
	log.Printf("sextant-mcp: connecting as auto-mint identity %q; pin a stable identity across sessions with $SEXTANT_CONTEXT=<context> (a registered agent context)", name)
	mint := m.mint
	if mint == nil {
		mint = m.mintAgent
	}
	return mint(ctx, name, agentDisplay(name))
}

// mintAgent self-enrolls a dedicated, non-active agent identity (the real mint
// behind resolve). On the concurrent-mint race — two starts of the same
// session — it reattaches to the context the winner wrote. When the bus is
// unreachable or its enrollment credential is missing it returns an actionable
// error rather than borrowing any existing identity; conn.get defers and
// retries, so the server self-heals once a bus is reachable.
func (m *connManager) mintAgent(ctx context.Context, name, display string) (clictx.ResolvedConn, error) {
	res, err := selfenroll.EnrollAgent(ctx, name, display, agentKind, *m.cf.url, *m.cf.store)
	if err != nil {
		var exists *selfenroll.ErrContextExists
		if errors.As(err, &exists) {
			if c, lerr := clictx.Load(name); lerr == nil {
				return clictx.ResolvedConn{Creds: c.Creds, URL: orStr(*m.cf.url, c.URL), Context: c.Name}, nil
			}
		}
		return clictx.ResolvedConn{}, fmt.Errorf("could not provision an agent identity: %w\n"+
			"sextant-mcp speaks as its own bus identity (never the operator's). Minting one needs the bus's enrollment credential at %s and a reachable bus.\n"+
			"start a local bus, or pin an identity with $SEXTANT_CONTEXT=<context> or $SEXTANT_CREDS=<file>",
			err, filepath.Join(*m.cf.store, "enroll.creds"))
	}
	return clictx.ResolvedConn{Creds: res.CredsPath, URL: res.URL, Context: res.Name}, nil
}

// restorePersistedContext re-pins the context_use choice carried across a resume
// (TASK-124, mode C) — but ONLY if it still resolves to an AGENT context. This
// mirrors use()'s guard: between sessions the saved context could have been
// deleted and recreated, or edited, as a human / client / unlabelled identity,
// and a resumed adapter must never assume a non-agent identity (ADR-0029). A
// missing or non-agent context is not re-pinned, so resolve() falls through to
// this session's own auto-minted identity. No lock: called at construction,
// before the server serves.
func (m *connManager) restorePersistedContext() {
	if m.state == nil {
		return
	}
	name, _ := m.state.snapshot()
	if name == "" {
		return
	}
	c, err := clictx.Load(name)
	if err != nil || c.Kind != agentKind {
		return
	}
	m.switched = name
}

// use switches this session's identity to an existing saved context (the
// context_use tool). It refuses a human context — the agent must never assume
// a person's identity, even when asked — and drops the held connection so the
// next call reconnects as the new identity.
func (m *connManager) use(name string) error {
	c, err := clictx.Load(name)
	if err != nil {
		names, _ := clictx.List()
		handles := make([]string, 0, len(names))
		for _, n := range names {
			handles = append(handles, n.Name)
		}
		return fmt.Errorf("%w\navailable contexts: %v", err, handles)
	}
	if c.Kind != agentKind {
		return fmt.Errorf("refusing to switch to %q (kind %q): context_use attaches only to agent identities, so the agent never speaks as a person or another client — pin a specific identity explicitly via $SEXTANT_CONTEXT if you mean to", name, c.Kind)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.switched = name
	// Persist the switch so a resumed process re-pins this identity instead of
	// reverting to the auto-mint id (TASK-124, mode C). nil in unit tests.
	if m.state != nil {
		m.state.setContext(name)
	}
	if m.client != nil {
		// Discard the old client the same way a drained-replace does (get()): run
		// onDiscard so its inbox drain stops AND the manual subscriptions bound to
		// it are cleared from the hub. Otherwise those stale entries would make the
		// next connect's restore skip rebinding them as "already active", leaving
		// the switched-to identity with no live relay (TASK-124).
		if m.onDiscard != nil {
			m.onDiscard(m.client)
		}
		_ = m.client.Close()
		m.client = nil
	}
	return nil
}

// agentContextName is the local handle for this session's identity. It is keyed
// on CLAUDE_CODE_SESSION_ID so a resumed session reattaches; absent that env
// (a non-Claude-Code host) — or with a session id that can't be a context
// handle — it is unique-per-process and not reattachable.
func agentContextName() (name string, persistent bool, err error) {
	if sid := os.Getenv(sessionEnv); sid != "" {
		if h := "claude-" + sid; clictx.ValidName(h) == nil {
			return h, true, nil
		}
		// An unusable session id can't be a context handle; fall through to a
		// fresh per-process identity rather than failing every call with a
		// misleading bus error.
	}
	suffix, err := randHex(6)
	if err != nil {
		return "", false, fmt.Errorf("agent identity: %w", err)
	}
	return "claude-" + suffix, false, nil
}

// agentDisplay is the friendly bus display name behind the (long) handle.
func agentDisplay(name string) string {
	const short = len("claude-") + 8
	if len(name) > short {
		return name[:short]
	}
	return name
}

// randHex returns n random bytes as hex. A crypto/rand failure fails loud
// rather than collapsing distinct sessions onto one empty-suffix handle.
func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random handle: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// get returns the held client, resolving identity and connecting if there is
// none (or the previous one drained). Errors are actionable: they name the
// resolution chain, or the URL tried and where it came from (ADR-0025).
func (m *connManager) get(ctx context.Context) (*sextant.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client != nil {
		select {
		case <-m.client.Drained():
			// The cached client drained: stop its inbox drain before replacing it,
			// so the old drain goroutine does not outlive the connection.
			if m.onDiscard != nil {
				m.onDiscard(m.client)
			}
			m.client = nil
		default:
			return m.client, nil
		}
	}

	rc, err := m.resolve(ctx)
	if err != nil {
		return nil, err
	}

	// Connect on the server-lifetime context, NOT the per-request ctx: the SDK
	// binds this connection's subscriptions (including the auto-inbox sub the M1
	// wake bridge drains) to the context passed here, so a request-scoped ctx
	// would tear the auto-inbox sub down the moment this tool call returned.
	connCtx := m.base
	if connCtx == nil {
		connCtx = context.Background()
	}
	c, err := sextant.Connect(connCtx, sextant.Options{
		CredsPath:    rc.Creds,
		URL:          rc.URL,
		ConnInfoPath: m.cf.connInfoPath(),
		Logf:         log.Printf,
	})
	if err != nil {
		return nil, fmt.Errorf("connect failed: %w\ntried url %s with creds %s", err, m.urlProvenance(rc), m.credsProvenance(rc))
	}
	m.client = c
	log.Printf("connected to %s as %s (%s)", rc.URL, c.DisplayName(), c.ID())
	// Record the identity we connected as, so the attest hook (a separate
	// process) follows it instead of re-resolving (ADR-0029/0030). We write the
	// resolved creds/url (rc) and the bus-stamped id we actually got (c.ID()).
	// Written on EVERY connect — including the reconnect a context_use switch
	// forces (use() drops the held client) — so a switch refreshes the file. A
	// write failure never fails the connect: the hook degrades to silent.
	if m.pluginData != "" {
		if err := attest.SaveIdentity(m.pluginData, m.sessionID, attest.Identity{
			Creds: rc.Creds,
			URL:   rc.URL,
			ID:    c.ID(),
		}); err != nil {
			log.Printf("sextant-mcp: record session identity for the attest hook: %v (hook will degrade)", err)
		}
	}
	// Start the inbox drain for this fresh client: bridges c.Inbox() into the
	// channel-wake path so a principal DM wakes the session (ADR-0030, M1).
	// Idempotent in the hub, so a transient retry path can't double-start it.
	if m.onConnect != nil {
		m.onConnect(c)
	}
	return c, nil
}

// urlProvenance names the URL that will be tried and its source, so a stale
// pinned URL is attributable at a glance (dogfood learning #3, ADR-0025).
func (m *connManager) urlProvenance(rc clictx.ResolvedConn) string {
	switch {
	case *m.cf.url != "":
		return fmt.Sprintf("%s (from --url)", *m.cf.url)
	case rc.URL != "":
		return fmt.Sprintf("%s (from context %q)", rc.URL, rc.Context)
	default:
		return fmt.Sprintf("discovered via %s (bus.json under --store)", m.cf.connInfoPath())
	}
}

func (m *connManager) credsProvenance(rc clictx.ResolvedConn) string {
	if rc.Context != "" {
		return fmt.Sprintf("%s (from context %q)", rc.Creds, rc.Context)
	}
	return fmt.Sprintf("%s (from --creds / $SEXTANT_CREDS)", rc.Creds)
}

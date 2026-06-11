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

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/internal/selfenroll"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
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

	mu     sync.Mutex
	client *sextant.Client
	// switched is the context Claude explicitly attached to via context_use;
	// empty means "use this session's own auto-provisioned identity".
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
	name, persistent := agentContextName()
	if persistent {
		if c, err := clictx.Load(name); err == nil {
			return clictx.ResolvedConn{Creds: c.Creds, URL: orStr(*m.cf.url, c.URL), Context: c.Name}, nil
		}
	}
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
	if c.Kind == "human" {
		return fmt.Errorf("refusing to switch to %q: it is a human identity, and the agent must not speak as a person", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.switched = name
	if m.client != nil {
		_ = m.client.Close()
		m.client = nil
	}
	return nil
}

// agentContextName is the local handle for this session's identity. It is keyed
// on CLAUDE_CODE_SESSION_ID so a resumed session reattaches; absent that env
// (a non-Claude-Code host) it is unique-per-process and not reattachable.
func agentContextName() (name string, persistent bool) {
	if sid := os.Getenv(sessionEnv); sid != "" {
		return "claude-" + sid, true
	}
	return "claude-" + randHex(6), false
}

// agentDisplay is the friendly bus display name behind the (long) handle.
func agentDisplay(name string) string {
	const short = len("claude-") + 8
	if len(name) > short {
		return name[:short]
	}
	return name
}

// randHex returns n random bytes as hex; crypto/rand failure is treated as
// fatal-by-empty (the caller's name still has its "claude-" prefix).
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
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
			m.client = nil
		default:
			return m.client, nil
		}
	}

	rc, err := m.resolve(ctx)
	if err != nil {
		return nil, err
	}

	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath:    rc.Creds,
		URL:          rc.URL,
		ConnInfoPath: m.cf.connInfoPath(),
		Logf:         log.Printf,
	})
	if err != nil {
		return nil, fmt.Errorf("connect failed: %v\ntried url %s with creds %s", err, m.urlProvenance(rc), m.credsProvenance(rc))
	}
	m.client = c
	log.Printf("connected to %s as %s (%s)", rc.URL, c.DisplayName(), c.ID())
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

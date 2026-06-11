package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/love-lena/sextant/internal/clictx"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sextant"
)

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
// get re-runs resolution, so a context minted mid-session (`sextant clients
// register --self`) heals the server without a restart.
type connManager struct {
	cf connFlags

	mu     sync.Mutex
	client *sextant.Client
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

	rc, err := clictx.Resolve(*m.cf.creds, *m.cf.url, *m.cf.context)
	if err != nil {
		if errors.Is(err, clictx.ErrNoIdentity) {
			return nil, fmt.Errorf("%w\nfresh machine? mint an identity — `sextant clients register --self --name <agent-name>` — then retry this tool call; no restart needed", err)
		}
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

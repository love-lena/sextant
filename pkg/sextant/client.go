// Package sextant is the Go SDK — the library you build a client with.
//
// Connect performs the connect handshake (ADR-0008, ADR-0010, ADR-0020):
// authenticate with this client's own credential, confirm the identity is known
// (issued, not retired) and hard-gate on the protocol epoch via clients.hello,
// and announce a soft warning if the local clock is far from the bus. It writes
// no registry entry — presence is derived from the connection itself — so a
// dropped connection alone never ends the client (the SDK reconnects; only an
// explicit drain does), and a clean Close just goes offline without retiring. The
// default drain behavior signals Drained(); the client's owner blocks on it and
// returns. (ADR-0010 frames the SDK as "ending the client" on drain; v1
// implements that as a signal rather than calling os.Exit from a library.)
//
// Identity (ADR-0012, ADR-0020): every client connects as its own verified
// identity, issued by the bus (`sextant clients register`) into a credentials
// file. The SDK does not invent identities — the client id (its registry key and
// frame author) is read from the credential itself, so what a client claims to be
// and what the bus authenticated it as cannot diverge.
package sextant

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
)

// Options configures Connect.
type Options struct {
	// CredsPath is this client's NATS credentials file — its verified identity,
	// issued by the bus (`sextant clients register`). Required. The client's id
	// (registry key and frame author) is the identity inside it.
	CredsPath string

	// URL is the bus address. If empty, it is read from ConnInfoPath.
	URL string
	// ConnInfoPath is the bus.json discovery file to read the URL from when URL
	// is not set explicitly.
	ConnInfoPath string

	// SkewTolerance overrides the clock-skew announce threshold.
	SkewTolerance time.Duration
	// Logf receives announcements; defaults to log.Printf.
	Logf func(string, ...any)
}

// Client is a connected Sextant client. Its kind is a property of the identity,
// set at issuance (`sextant clients register --kind`), not at connect — so a
// connecting client carries no kind of its own.
type Client struct {
	nc          *nats.Conn
	id          string
	displayName string
	skewTol     time.Duration
	logf        func(string, ...any)

	drainOnce sync.Once
	drained   chan struct{}

	// subsMu guards subs. Subscriptions register themselves on creation and
	// deregister on teardown; the reconnect handler snapshots the set under the
	// lock so it can re-establish each relay without holding the lock. The set
	// is keyed by the subscription itself — its sub-id rotates on every resume
	// pass. Write operations (register, deregister) are infrequent (one per
	// Subscribe/Stop); the reconnect snapshot is a single copy under the lock.
	subsMu sync.Mutex
	subs   map[*subscription]struct{}
}

// Connect dials the bus and runs the connect handshake. ctx governs the
// post-dial handshake (clients.hello, drain-subscription flush); the dial itself
// uses the NATS client's own connect timeout, as nats.Connect has no
// context-aware form.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	if opts.CredsPath == "" {
		return nil, errors.New("sextant: no credentials (set Options.CredsPath; issue one with `sextant clients register <name>`)")
	}
	url := opts.URL
	if url == "" && opts.ConnInfoPath != "" {
		info, err := conninfo.Read(opts.ConnInfoPath)
		if err != nil {
			return nil, err
		}
		url = info.URL
	}
	if url == "" {
		return nil, errors.New("sextant: no bus URL (set Options.URL or Options.ConnInfoPath)")
	}

	// Identity comes from the credential, not the caller — the id (a bus-minted
	// ULID) and the human display_name are both read from the JWT the bus
	// authenticates, so neither can diverge from what was minted.
	id, displayName, err := identityFromCreds(opts.CredsPath)
	if err != nil {
		return nil, err
	}
	tol := opts.SkewTolerance
	if tol == 0 {
		tol = wire.SkewTolerance
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}

	c := &Client{id: id, displayName: displayName, skewTol: tol, logf: logf, drained: make(chan struct{})}

	nc, err := nats.Connect(
		url,
		nats.UserCredentials(opts.CredsPath),
		nats.Name(id),
		// Use a per-client inbox so call replies land under _INBOX.<id>, which is
		// the only inbox this credential may subscribe to. Without it the client
		// would use the shared _INBOX prefix the allow-list denies — every call
		// would time out — and a shared inbox would let other clients eavesdrop on
		// our replies. Must match wireapi.InboxPrefix / clientPermissions.
		nats.CustomInboxPrefix(wireapi.InboxPrefix(id)),
		nats.MaxReconnects(-1), // connection-loss != exit; reconnect forever
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				c.logf("sextant: disconnected (%v); reconnecting", err)
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) {
			// Re-establish every active subscription's server-side relay so the
			// subscriber keeps receiving messages (ADR-0027). Runs synchronously on
			// the NATS reconnect goroutine; each re-establish is deadline-bounded.
			// Runs first, before the "reconnected" log, so the log fires only after
			// subscriptions are live — callers waiting on that log see a ready bus.
			c.reestablishSubs()
			c.logf("sextant: reconnected to the bus")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("sextant: connect: %w", err)
	}
	c.nc = nc

	// The connect handshake runs entirely through Wire API calls: hello confirms
	// the identity and folds the protocol-epoch hard-gate, and watchDrain sets up
	// the drain subscription. The SDK never touches the backend directly (ADR-0019).
	if err := c.hello(ctx, tol); err != nil {
		nc.Close()
		return nil, err
	}
	if err := c.watchDrain(ctx); err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

// identityFromCreds reads the client id and display_name from its credentials
// file. The id is the user JWT's name — a bus-minted ULID; the display_name is a
// JWT tag. It is the same JWT the bus verifies on connect, so what is read here
// is exactly what the bus authenticates — a client cannot register or send under
// an identity it did not authenticate as (editing either would break the JWT
// signature).
func identityFromCreds(path string) (id, displayName string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("sextant: read credentials %s: %w", path, err)
	}
	tok, err := jwt.ParseDecoratedJWT(b)
	if err != nil {
		return "", "", fmt.Errorf("sextant: parse credentials %s: %w", path, err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return "", "", fmt.Errorf("sextant: decode credentials %s: %w", path, err)
	}
	if uc.Name == "" {
		return "", "", fmt.Errorf("sextant: credentials %s carry no client id (user name)", path)
	}
	for _, tag := range uc.Tags {
		if name, ok := wireapi.DecodeDisplayNameTag(tag); ok {
			displayName = name
			break
		}
	}
	return uc.Name, displayName, nil
}

// hello is the connect handshake (ADR-0020): a single clients.hello call that
// confirms this client is a known (issued, not retired) identity and folds the
// protocol-epoch hard-gate into one round-trip. The bus returns its epoch, which
// the SDK exact-matches (mismatch fails loud, ADR-0010), and the bus-stamped
// server time, which the SDK clock-skew-checks (a soft announce, not a gate). It
// writes nothing: presence is derived from this connection, so there is no
// registry entry to create here and none to remove on Close.
func (c *Client) hello(ctx context.Context, tol time.Duration) error {
	var out wireapi.HelloOutput
	if err := c.call(ctx, wireapi.OpClientsHello, wireapi.HelloInput{}, &out); err != nil {
		return err
	}
	if err := wire.CheckEpoch(wire.Epoch, out.BusEpoch); err != nil {
		return fmt.Errorf("%w (rebuild the client against the bus's protocol)", err)
	}
	if t, perr := time.Parse(time.RFC3339, out.ServerTime); perr == nil {
		if skew := clockSkew(time.Now(), t); skew.Abs() > tol {
			c.logf("sextant: clock skew %s vs the bus exceeds %s; messages may be rejected — sync NTP", skew, tol)
		}
	}
	return nil
}

func (c *Client) watchDrain(ctx context.Context) error {
	if _, err := c.nc.Subscribe(wireapi.DeliverSubject(c.id, wireapi.DrainSubID), func(*nats.Msg) {
		c.logf("sextant: drain received; winding down")
		c.drainOnce.Do(func() { close(c.drained) })
	}); err != nil {
		return fmt.Errorf("sextant: subscribe drain: %w", err)
	}
	// Flush so the subscription is registered server-side before Connect returns:
	// otherwise a drain broadcast published immediately after connect can race
	// ahead of our still-buffered SUB and be missed. Honor the caller's deadline
	// when it set one; otherwise fall back to the connection's own flush timeout.
	flush := c.nc.Flush
	if _, ok := ctx.Deadline(); ok {
		flush = func() error { return c.nc.FlushWithContext(ctx) }
	}
	if err := flush(); err != nil {
		return fmt.Errorf("sextant: flush drain subscription: %w", err)
	}
	return nil
}

// Drained is closed when the bus broadcasts a cooperative drain. The standard
// client pattern blocks on it and then returns (calling Close).
func (c *Client) Drained() <-chan struct{} { return c.drained }

// ID is this client's identity: the bus-minted ULID (its registry key and frame
// author).
func (c *Client) ID() string { return c.id }

// DisplayName is this client's human-readable label, minted with its credential.
// It may be empty for a credential minted without one.
func (c *Client) DisplayName() string { return c.displayName }

// Close closes the connection. It does NOT retire the identity (ADR-0020): a
// clean close just drops presence to offline — the durable identity persists, so
// the same client can reconnect later under the same id. Decommissioning for good
// is an explicit operator `clients retire`, never an implicit consequence of Close.
func (c *Client) Close() error {
	c.nc.Close()
	return nil
}

func clockSkew(local, bus time.Time) time.Duration { return local.Sub(bus) }

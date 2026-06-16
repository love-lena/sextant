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
	"github.com/love-lena/sextant/pkg/sx"
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

	// principalMu guards principal: the bus-designated principal ULID (ADR-0030).
	// It is set from the connect handshake (hello) and advanced by a
	// principal.watch delivery, so a connected client observes a re-designation
	// without reconnecting. Read with Principal().
	principalMu sync.RWMutex
	principal   string

	// subsMu guards subs. Subscriptions register themselves on creation and
	// deregister on teardown; the reconnect handler snapshots the set under the
	// lock so it can re-establish each relay without holding the lock. The set
	// is keyed by the subscription itself — its sub-id rotates on every resume
	// pass. Write operations (register, deregister) are infrequent (one per
	// Subscribe/Stop); the reconnect snapshot is a single copy under the lock.
	subsMu sync.Mutex
	subs   map[*subscription]struct{}

	// closed signals Close: an in-flight resume pass stops at its next
	// subscription boundary instead of rotating relays on a dying client.
	closed    chan struct{}
	closeOnce sync.Once
	// passWG tracks in-flight resume-pass goroutines (startResumePass) so Close
	// can drain them with a bounded wait — no pass outlives the client silently.
	//
	// passMu makes spawning and Close atomic with respect to each other: a
	// spawn holds it across {closed re-check, passWG.Add}, and Close holds it
	// across close(closed). That yields the ordering sync.WaitGroup requires —
	// every Add happens-before close(closed), which happens-before
	// passWG.Wait — so an Add can never race the Wait, and no pass goroutine
	// can spawn once Close has decided to drain. The expensive spawn inputs
	// (the reconnect count, the registry snapshot) are read outside the lock.
	passMu sync.Mutex
	passWG sync.WaitGroup
	// passClaimed (under passMu) is 1 + the highest pass token a resume pass
	// has been spawned for; 0 means none yet. nats.go bumps the reconnect count
	// under the connection lock and only then queues the handler on the serial
	// async dispatcher, so after two rapid reconnects both handlers can read
	// the same, latest count: the claim admits exactly one pass per token — the
	// sibling skips, runs nothing, and logs nothing.
	passClaimed uint64

	// passSpawnHook is a test seam: when non-nil, startResumePass calls it
	// between reading its spawn inputs and entering the passMu critical
	// section, so a test can hold a spawn exactly inside the window that races
	// Close. Always nil in production.
	passSpawnHook func()

	// inbox is the inbound inbox channel: messages published to
	// msg.client.<self> are delivered here by the auto-subscription that
	// Connect establishes (ADR-0030). Buffered so a burst of inbound inbox messages
	// does not block the SDK delivery goroutine; a slow consumer loses
	// messages once the buffer is full (identical to a slow explicit
	// Subscribe handler). Read with Inbox().
	inbox chan Message
	// inboxSub is the auto-inbox subscription established by subscribeInbox. Close
	// tears it down synchronously (before nc.Close) so the bus-side relay
	// goroutines do not outlive the client. Nil when subscribeInbox has not
	// run (e.g. in tests that construct a Client directly without Connect).
	inboxSub *subscription
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

	c := &Client{
		id:          id,
		displayName: displayName,
		skewTol:     tol,
		logf:        logf,
		drained:     make(chan struct{}),
		closed:      make(chan struct{}),
		inbox:       make(chan Message, 64),
	}

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
			// subscriber keeps receiving messages (ADR-0027). The pass runs on its
			// own goroutine — each rotation is deadline-bounded, but a pass is
			// unbounded in aggregate (N subscriptions × 10s against a sick bus),
			// and this callback shares the async dispatcher with every other
			// notification, so running it inline would wedge later disconnect and
			// reconnect notices behind it. The pass logs "reconnected to the bus"
			// itself, only once all relays are live (end of a completed,
			// non-superseded pass), so callers waiting on that log see a ready bus.
			c.startResumePass()
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
	if err := c.subscribeInbox(ctx); err != nil {
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
	// Discover the principal designation in the same round-trip (ADR-0030). A
	// connected client keeps it current with WatchPrincipal; a one-shot read is
	// GetPrincipal.
	c.setPrincipal(out.Principal)
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

// Inbox returns the inbound inbox channel: messages published to this
// client's own inbox subject (msg.client.<self>) arrive here without any explicit
// Subscribe call. The channel is buffered (64 messages); a receiver that falls
// behind will have messages dropped — the same behavior as a slow explicit
// Subscribe handler. A sender messaging this client's inbox need not know whether
// the client is subscribed: the bus delivers to the relay the auto-subscription
// establishes on connect, and the SDK fans the frame into this channel.
func (c *Client) Inbox() <-chan Message { return c.inbox }

// subscribeInbox sets up the auto-subscription to msg.client.<self>. It is called
// once during Connect, after the hello handshake, so a client is reachable by
// direct message the instant it exists (ADR-0030). The subscription pointer is
// stored in c.inboxSub so Close can call teardown synchronously (before nc.Close)
// ensuring the bus-side relay goroutines exit before the connection drops.
//
// The handler delivers into c.inbox with a non-blocking send: a full buffer drops
// the message rather than stalling the SDK delivery goroutine.
//
// The reconnect re-establishment path (startResumePass → reestablishSubs) covers
// this subscription exactly like any explicit Subscribe call — it is registered in
// the same c.subs set — so AC#3 (survives reconnect) is satisfied automatically.
func (c *Client) subscribeInbox(ctx context.Context) error {
	subject := sx.ClientSubject(c.id)
	sub, err := c.Subscribe(ctx, subject, func(m Message) {
		select {
		case c.inbox <- m:
		default:
			c.logf("sextant: inbox channel full; dropping message on %s from %s", subject, m.Frame.Author)
		}
	})
	if err != nil {
		return fmt.Errorf("sextant: auto-subscribe inbox (%s): %w", subject, err)
	}
	c.inboxSub = sub.(*subscription)
	return nil
}

// Principal is the bus-designated principal's client ULID (ADR-0030) as the
// client last learned it: at connect (the hello handshake) and from any
// principal.watch delivery since. A client compares an inbound message's
// bus-stamped author against this to decide whether the message is its
// principal's — operator-equivalent input. Empty means no principal is
// designated. It never blocks; it reads the locally cached value.
func (c *Client) Principal() string {
	c.principalMu.RLock()
	defer c.principalMu.RUnlock()
	return c.principal
}

// setPrincipal records the latest principal designation (from hello or a watch
// delivery). It is the single writer path for the cached value.
func (c *Client) setPrincipal(p string) {
	c.principalMu.Lock()
	c.principal = p
	c.principalMu.Unlock()
}

// Close closes the connection. It does NOT retire the identity (ADR-0020): a
// clean close just drops presence to offline — the durable identity persists, so
// the same client can reconnect later under the same id. Decommissioning for good
// is an explicit operator `clients retire`, never an implicit consequence of Close.
//
// Close also winds down any in-flight resume pass: the closed signal stops it
// at its next subscription boundary, and closing the connection fails its
// in-flight rotation call promptly. The drain wait is bounded and loud (never
// a silent hang): if a pass somehow does not stop in time, Close logs and
// returns — the pass's own per-rotation deadlines still bound its exit.
func (c *Client) Close() error {
	// Tear down the auto-inbox subscription synchronously before closing nc.
	// teardown (via once.Do) calls OpSubscriptionStop while nc is still open,
	// stopping the bus-side relay and its goroutines before they would
	// otherwise linger until nc.Close()'s disconnection. After teardown we
	// cancel the bridge goroutine's context so it exits rather than leaking;
	// the bridge's own teardown call is a no-op (once.Do) if it races our
	// synchronous one. deregisterSub is idempotent across both paths.
	if c.inboxSub != nil {
		c.inboxSub.stopped.Store(true)
		c.inboxSub.teardown()
		c.inboxSub.cancel()
		c.deregisterSub(c.inboxSub)
	}
	// Under passMu so the closed signal is atomic with any in-flight spawn's
	// {closed re-check, passWG.Add}: after this critical section, no resume
	// pass can be added to the WaitGroup the drain below waits on.
	c.passMu.Lock()
	c.closeOnce.Do(func() { close(c.closed) })
	c.passMu.Unlock()
	c.nc.Close()
	done := make(chan struct{})
	go func() {
		c.passWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		c.logf("sextant: a resume pass did not wind down within 15s of Close; not waiting further")
	}
	return nil
}

func clockSkew(local, bus time.Time) time.Duration { return local.Sub(bus) }

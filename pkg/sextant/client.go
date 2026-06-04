// Package sextant is the Go SDK — the library you build a client with.
//
// Connect performs the connect handshake (ADR-0008, ADR-0010): authenticate
// with this client's own credential, hard-gate on the protocol epoch, register
// in the clients registry, and announce a soft warning if the local clock is
// far from the bus. A dropped connection alone never ends the client — the SDK
// reconnects; only an explicit drain does. The default drain behavior signals
// Drained(); the client's owner blocks on it and returns. (ADR-0010 frames the
// SDK as "ending the client" on drain; v1 implements that as a signal +
// best-effort registry-leave rather than calling os.Exit from a library —
// flagged for review.)
//
// Identity (ADR-0012): every client connects as its own verified identity,
// minted out-of-band by `sextant token <id>` into a credentials file. The SDK
// does not invent identities — the client id (its registry key and, later, its
// envelope sender) is read from the credential itself, so what a client claims
// to be and what the bus authenticated it as cannot diverge.
package sextant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/love-lena/sextant/pkg/conninfo"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// sdkVersion is recorded in the registry record. (A real version surface comes
// later; see the versioning ADR.)
const sdkVersion = "0.0.0-dev"

// Options configures Connect.
type Options struct {
	// CredsPath is this client's NATS credentials file — its verified identity,
	// minted out-of-band by `sextant token <id>`. Required. The client's id
	// (registry key and envelope sender) is the identity inside it.
	CredsPath string

	// URL is the bus address. If empty, it is read from ConnInfoPath.
	URL string
	// ConnInfoPath is the bus.json discovery file to read the URL from when URL
	// is not set explicitly.
	ConnInfoPath string

	// Kind is what this client is (e.g. "harness", "coordinator"), recorded in
	// the registry. Default "client".
	Kind string
	// SkewTolerance overrides the clock-skew announce threshold.
	SkewTolerance time.Duration
	// Logf receives announcements; defaults to log.Printf.
	Logf func(string, ...any)
}

// Client is a connected Sextant client.
type Client struct {
	nc      *nats.Conn
	js      jetstream.JetStream
	id      string
	kind    string
	skewTol time.Duration
	logf    func(string, ...any)

	drainOnce sync.Once
	drained   chan struct{}
}

// Connect dials the bus and runs the connect handshake. ctx governs the
// post-dial handshake (epoch read, registry write, drain-subscription flush);
// the dial itself uses the NATS client's own connect timeout, as nats.Connect
// has no context-aware form.
func Connect(ctx context.Context, opts Options) (*Client, error) {
	if opts.CredsPath == "" {
		return nil, errors.New("sextant: no credentials (set Options.CredsPath; mint one with `sextant token <id>`)")
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

	// Identity comes from the credential, not the caller — the id is whatever
	// the bus will authenticate this connection as.
	id, err := identityFromCreds(opts.CredsPath)
	if err != nil {
		return nil, err
	}
	kind := opts.Kind
	if kind == "" {
		kind = "client"
	}
	tol := opts.SkewTolerance
	if tol == 0 {
		tol = wire.SkewTolerance
	}
	logf := opts.Logf
	if logf == nil {
		logf = log.Printf
	}

	c := &Client{id: id, kind: kind, skewTol: tol, logf: logf, drained: make(chan struct{})}

	nc, err := nats.Connect(
		url,
		nats.UserCredentials(opts.CredsPath),
		nats.Name(id),
		nats.MaxReconnects(-1), // connection-loss != exit; reconnect forever
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				c.logf("sextant: disconnected (%v); reconnecting", err)
			}
		}),
		nats.ReconnectHandler(func(*nats.Conn) {
			c.logf("sextant: reconnected to the bus")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("sextant: connect: %w", err)
	}
	c.nc = nc

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("sextant: jetstream: %w", err)
	}
	c.js = js

	if err := c.checkEpoch(ctx); err != nil {
		nc.Close()
		return nil, err
	}
	if err := c.register(ctx, tol); err != nil {
		nc.Close()
		return nil, err
	}
	if err := c.watchDrain(ctx); err != nil {
		nc.Close()
		return nil, err
	}
	return c, nil
}

// identityFromCreds reads the client id from its credentials file: the name of
// the user JWT minted by `sextant token <id>`. It is the same JWT the bus
// verifies on connect, so the id read here is exactly the identity the bus
// authenticates — a client cannot register or send under a name it did not
// authenticate as (editing the name would break the JWT signature).
func identityFromCreds(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("sextant: read credentials %s: %w", path, err)
	}
	tok, err := jwt.ParseDecoratedJWT(b)
	if err != nil {
		return "", fmt.Errorf("sextant: parse credentials %s: %w", path, err)
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return "", fmt.Errorf("sextant: decode credentials %s: %w", path, err)
	}
	if uc.Name == "" {
		return "", fmt.Errorf("sextant: credentials %s carry no client id (user name)", path)
	}
	return uc.Name, nil
}

// checkEpoch reads the bus's protocol epoch from the public meta bucket and
// exact-matches it; mismatch fails loud (ADR-0010).
func (c *Client) checkEpoch(ctx context.Context) error {
	meta, err := c.js.KeyValue(ctx, sx.BucketMeta)
	if err != nil {
		return fmt.Errorf("sextant: open %s: %w", sx.BucketMeta, err)
	}
	entry, err := meta.Get(ctx, sx.MetaKeyEpoch)
	if err != nil {
		return fmt.Errorf("sextant: read bus epoch: %w", err)
	}
	busEpoch, err := strconv.Atoi(string(entry.Value()))
	if err != nil {
		return fmt.Errorf("sextant: bad bus epoch %q: %w", entry.Value(), err)
	}
	if err := wire.CheckEpoch(wire.Epoch, busEpoch); err != nil {
		return fmt.Errorf("%w (rebuild the client against the bus's protocol)", err)
	}
	return nil
}

// registryRecord is this client's entry in the clients registry. (The full
// schema — subscriptions, heartbeat cadence, liveness threshold — is an open
// question; this is the minimal connect-time record.)
type registryRecord struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Epoch       int    `json:"epoch"`
	SDK         string `json:"sdk"`
	ConnectedAt string `json:"connected_at"`
}

func (c *Client) register(ctx context.Context, tol time.Duration) error {
	clients, err := c.js.KeyValue(ctx, sx.BucketClients)
	if err != nil {
		return fmt.Errorf("sextant: open %s: %w", sx.BucketClients, err)
	}
	rec := registryRecord{
		ID:          c.id,
		Kind:        c.kind,
		Epoch:       wire.Epoch,
		SDK:         sdkVersion,
		ConnectedAt: time.Now().UTC().Format(time.RFC3339),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("sextant: marshal registry record: %w", err)
	}
	if _, err := clients.Put(ctx, c.id, b); err != nil {
		return fmt.Errorf("sextant: register: %w", err)
	}
	// The registry write is bus-stamped; compare to the local clock and announce
	// (soft, not a gate) if the skew is large (ADR-0010).
	if entry, err := clients.Get(ctx, c.id); err == nil {
		if skew := clockSkew(time.Now(), entry.Created()); skew.Abs() > tol {
			c.logf("sextant: clock skew %s vs the bus exceeds %s; messages may be rejected — sync NTP", skew, tol)
		}
	}
	return nil
}

func (c *Client) watchDrain(ctx context.Context) error {
	if _, err := c.nc.Subscribe(sx.SubjectDrain, func(*nats.Msg) {
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

// ID is this client's identity (registry key and envelope sender).
func (c *Client) ID() string { return c.id }

// Close leaves the clients registry (best-effort) and closes the connection.
func (c *Client) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if clients, err := c.js.KeyValue(ctx, sx.BucketClients); err == nil {
		_ = clients.Delete(ctx, c.id) // best-effort registry leave
	}
	c.nc.Close()
	return nil
}

func clockSkew(local, bus time.Time) time.Duration { return local.Sub(bus) }

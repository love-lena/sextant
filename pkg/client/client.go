package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Client is a sextant bus client. Connect or ConnectWithConfig builds
// one; Close releases the underlying NATS connection. A Client is safe
// for concurrent use; the underlying *nats.Conn handles multiplexing.
type Client struct {
	cfg    Config
	nc     *nats.Conn
	js     jetstream.JetStream
	doneCh chan struct{} // closed exactly once by Close

	mu       sync.Mutex
	closed   bool
	stoppers []*stopRegistration // active Subscribe / WatchKV cleanups
}

// stopRegistration is one active background worker — a Subscribe loop
// or a WatchKV watcher — registered on the Client. Close iterates and
// calls Stop on every registration so callers don't have to cancel
// every Subscribe ctx individually.
type stopRegistration struct {
	once sync.Once
	stop func()
}

// register adds a cleanup function to the Client's tracked workers and
// returns the registration handle the worker can use to deregister on
// natural exit. Callers MUST invoke handle.run() once the worker has
// fully torn itself down so it doesn't keep showing up in Close.
func (c *Client) register(stop func()) *stopRegistration {
	reg := &stopRegistration{stop: stop}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stoppers = append(c.stoppers, reg)
	return reg
}

// run invokes the registration's stop function at most once.
func (r *stopRegistration) run() {
	r.once.Do(r.stop)
}

// deregister drops reg from the Client's tracked-worker list. Safe to
// call multiple times; the second call is a no-op.
func (c *Client) deregister(reg *stopRegistration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, s := range c.stoppers {
		if s == reg {
			c.stoppers = append(c.stoppers[:i], c.stoppers[i+1:]...)
			return
		}
	}
}

// Option configures a Client at construction time. Reserved for future
// knobs (tracer injection, custom NATS option overrides); M4 ships no
// concrete options but keeps the variadic for API stability.
type Option interface {
	apply(*clientOptions)
}

type clientOptions struct {
	natsOpts []nats.Option
}

type natsOptOpt struct{ opts []nats.Option }

func (o natsOptOpt) apply(c *clientOptions) { c.natsOpts = append(c.natsOpts, o.opts...) }

// WithExtraNATSOptions appends nats.Option values to the connection.
// Primarily for tests; production code should not need this.
func WithExtraNATSOptions(opts ...nats.Option) Option { return natsOptOpt{opts: opts} }

// Connect loads ~/.config/sextant/client.toml (or the supplied path) and
// returns a connected Client. Pass an empty configPath to use
// DefaultConfigPath.
func Connect(ctx context.Context, configPath string, opts ...Option) (*Client, error) {
	if configPath == "" {
		p, err := DefaultConfigPath()
		if err != nil {
			return nil, err
		}
		configPath = p
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return ConnectWithConfig(ctx, cfg, opts...)
}

// ConnectWithConfig dials NATS using an already-parsed Config.
func ConnectWithConfig(ctx context.Context, cfg Config, opts ...Option) (*Client, error) {
	normalized, err := cfg.validateAndFill()
	if err != nil {
		return nil, err
	}

	var co clientOptions
	for _, o := range opts {
		o.apply(&co)
	}

	natsOpts := []nats.Option{
		nats.Name("sextant-client-go"),
		nats.Timeout(normalized.Client.ConnectTimeout.AsDuration()),
		// MaxReconnects(-1) = unlimited. ReconnectWait + jitter values
		// pinned in specs/components/client-libraries.md §"Shared
		// concerns".
		nats.MaxReconnects(-1),
		nats.ReconnectWait(500 * time.Millisecond),
		nats.ReconnectJitter(100*time.Millisecond, 500*time.Millisecond),
	}
	switch {
	case normalized.Operator.CredsPath != "":
		natsOpts = append(natsOpts, nats.UserCredentials(normalized.Operator.CredsPath))
	default:
		natsOpts = append(natsOpts, nats.UserInfo(normalized.Operator.User, normalized.Operator.Password))
	}
	// Caller-supplied overrides last, so tests can pin behavior.
	natsOpts = append(natsOpts, co.natsOpts...)

	nc, err := nats.Connect(normalized.NATS.URL, natsOpts...)
	if err != nil {
		return nil, fmt.Errorf("client: connect %s: %w", normalized.NATS.URL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("client: jetstream.New: %w", err)
	}

	c := &Client{
		cfg:    normalized,
		nc:     nc,
		js:     js,
		doneCh: make(chan struct{}),
	}

	// Bind connection lifecycle to ctx: if ctx cancels before Close, we
	// close the underlying conn. The goroutine also exits when Close
	// runs first (via doneCh) so it does not outlive the Client.
	if ctx != nil {
		go c.watchCtx(ctx)
	}

	return c, nil
}

// Close releases the underlying NATS connection and stops every active
// Subscribe loop and WatchKV watcher created against this Client.
// Idempotent: a second call is a no-op.
//
// Subscribe / WatchKV channels close cleanly even if the caller passed
// a long-lived context (e.g. context.Background()). After Close returns,
// every previously-returned channel is closed.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	// Snapshot the registrations under the lock, then drop it so the
	// stop functions can take the lock themselves (deregister) without
	// deadlocking.
	regs := c.stoppers
	c.stoppers = nil
	c.mu.Unlock()

	for _, r := range regs {
		r.run()
	}
	if c.doneCh != nil {
		close(c.doneCh)
	}
	if c.nc != nil {
		c.nc.Close()
	}
	return nil
}

// Conn exposes the underlying *nats.Conn. Test-only; production callers
// should drive everything through Client's typed surface. Kept exported
// so integration tests can inject envelopes without bringing a second
// connection up.
func (c *Client) Conn() *nats.Conn { return c.nc }

// JetStream exposes the JetStream context. Test-only — see Conn.
func (c *Client) JetStream() jetstream.JetStream { return c.js }

// Config returns a copy of the Config the Client was built with.
func (c *Client) Config() Config { return c.cfg }

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// watchCtx tears the Client down when the caller's ctx is canceled, OR
// exits silently if Close has already fired (signaled via doneCh). This
// ensures the goroutine never outlives the Client even when ctx is
// never canceled.
func (c *Client) watchCtx(ctx context.Context) {
	select {
	case <-ctx.Done():
		_ = c.Close()
	case <-c.doneCh:
	}
}

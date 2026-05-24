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
	cfg Config
	nc  *nats.Conn
	js  jetstream.JetStream

	mu     sync.Mutex
	closed bool
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
		cfg: normalized,
		nc:  nc,
		js:  js,
	}

	// Bind connection lifecycle to ctx: if ctx cancels before Close, we
	// close the underlying conn. This matches the "first arg is ctx"
	// rule for things that own background work.
	if ctx != nil {
		go c.watchCtx(ctx)
	}

	return c, nil
}

// Close releases the underlying NATS connection. Idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
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

func (c *Client) watchCtx(ctx context.Context) {
	<-ctx.Done()
	_ = c.Close()
}

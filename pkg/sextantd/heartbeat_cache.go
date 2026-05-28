package sextantd

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

const heartbeatSubject = "agents.*.heartbeat"

// HeartbeatCache subscribes the core NATS heartbeat wildcard and
// records the latest observed timestamp per agent. Consumers
// (prompt_agent) read LastSeen as a freshness signal.
type HeartbeatCache struct {
	nc    *nats.Conn
	nowFn func() time.Time

	mu       sync.RWMutex
	lastSeen map[uuid.UUID]time.Time
	sub      *nats.Subscription
}

// HeartbeatCacheOption is a functional option for NewHeartbeatCache.
type HeartbeatCacheOption func(*HeartbeatCache)

// WithClock injects a custom clock into the cache. Used by tests to fix
// the time so LastSeen assertions are deterministic.
func WithClock(now func() time.Time) HeartbeatCacheOption {
	return func(c *HeartbeatCache) { c.nowFn = now }
}

// NewHeartbeatCache subscribes to agents.*.heartbeat and returns a running
// cache. Caller is responsible for calling Stop() during shutdown — the
// cache does not own the *nats.Conn, only its subscription.
//
// Returns an error if nc is nil or if the subscription fails. On error the
// cache is not started; caller doesn't need to call Stop.
func NewHeartbeatCache(nc *nats.Conn, opts ...HeartbeatCacheOption) (*HeartbeatCache, error) {
	if nc == nil {
		return nil, fmt.Errorf("heartbeat cache: nats conn is nil")
	}
	c := &HeartbeatCache{
		nc:       nc,
		nowFn:    time.Now,
		lastSeen: make(map[uuid.UUID]time.Time),
	}
	for _, opt := range opts {
		opt(c)
	}
	sub, err := nc.Subscribe(heartbeatSubject, c.handle)
	if err != nil {
		return nil, fmt.Errorf("heartbeat cache: subscribe %s: %w", heartbeatSubject, err)
	}
	c.mu.Lock()
	c.sub = sub
	c.mu.Unlock()
	return c, nil
}

// LastSeen returns the last time a heartbeat was received for agentID,
// and true. Returns (zero, false) if no heartbeat has been seen for
// this agent.
func (c *HeartbeatCache) LastSeen(agentID uuid.UUID) (time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	t, ok := c.lastSeen[agentID]
	return t, ok
}

// Stop unsubscribes from the heartbeat subject. Idempotent; safe to call
// after a fatal error returned from NewHeartbeatCache (in which case it
// is a no-op).
func (c *HeartbeatCache) Stop() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	sub := c.sub
	c.sub = nil
	c.mu.Unlock()
	if sub == nil {
		return nil
	}
	if err := sub.Unsubscribe(); err != nil && !errors.Is(err, nats.ErrConnectionClosed) {
		return fmt.Errorf("heartbeat cache: unsubscribe: %w", err)
	}
	return nil
}

// handle is the per-message NATS callback. It decodes the envelope and
// records the current time for the agent UUID.
//
// Errors are logged, not propagated — the NATS dispatcher has nowhere
// to send them.
func (c *HeartbeatCache) handle(msg *nats.Msg) {
	var env sextantproto.Envelope
	if err := json.Unmarshal(msg.Data, &env); err != nil {
		log.Printf("sextantd: heartbeat cache: decode envelope on %s: %v", msg.Subject, err)
		return
	}
	if env.Kind != sextantproto.KindHeartbeat {
		return
	}
	var payload sextantproto.HeartbeatPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		log.Printf("sextantd: heartbeat cache: decode payload on %s: %v", msg.Subject, err)
		return
	}
	if payload.AgentUUID == uuid.Nil {
		return
	}
	now := c.nowFn()
	c.mu.Lock()
	c.lastSeen[payload.AgentUUID] = now
	c.mu.Unlock()
}

package rpc

import (
	"sync"
	"time"
)

// idempotencyTTL is the spec-pinned 60s window inside which a repeated
// (verb, idempotency_key) pair returns the cached response without
// re-executing the handler. specs/protocols/rpc-catalog.md §"Idempotency".
const idempotencyTTL = 60 * time.Second

// idemCache is the server's idempotency cache. Keys are
// (verb, idempotency_key); values are the cached reply envelopes that
// would be re-published byte-for-byte on a repeat request.
//
// In-memory only; a daemon restart drops the cache. The 60s TTL is
// short enough that any in-flight retry storm self-resolves either
// from the cache or by re-execution after the window expires.
type idemCache struct {
	mu  sync.Mutex
	now func() time.Time
	ttl time.Duration
	rec map[idemKey]idemEntry
}

type idemKey struct {
	verb string
	key  string
}

type idemEntry struct {
	reply     []byte
	expiresAt time.Time
}

// newIdemCache returns a cache that prunes entries older than the TTL.
// The now closure is injectable for tests.
func newIdemCache(now func() time.Time, ttl time.Duration) *idemCache {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = idempotencyTTL
	}
	return &idemCache{
		now: now,
		ttl: ttl,
		rec: make(map[idemKey]idemEntry),
	}
}

// Lookup returns the cached reply for (verb, key) and true if it is
// still within the TTL window. A cache miss or expired entry returns
// (nil, false). Lookup also opportunistically evicts the expired entry
// it just observed.
func (c *idemCache) Lookup(verb, key string) ([]byte, bool) {
	if key == "" {
		return nil, false
	}
	k := idemKey{verb: verb, key: key}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.rec[k]
	if !ok {
		return nil, false
	}
	if !c.now().Before(entry.expiresAt) {
		delete(c.rec, k)
		return nil, false
	}
	return entry.reply, true
}

// Store records (verb, key) → reply with an expiry one TTL into the
// future. Storing under an empty key is a no-op (the spec requires the
// caller to supply an idempotency key on every request; rejecting the
// request is the caller's job, but Store should not crash on it).
func (c *idemCache) Store(verb, key string, reply []byte) {
	if key == "" {
		return
	}
	k := idemKey{verb: verb, key: key}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rec[k] = idemEntry{
		reply:     append([]byte(nil), reply...),
		expiresAt: c.now().Add(c.ttl),
	}
}

// Sweep removes every entry whose expiresAt is in the past. Called by
// the background pruner; safe to call from tests directly.
func (c *idemCache) Sweep() {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, entry := range c.rec {
		if !now.Before(entry.expiresAt) {
			delete(c.rec, k)
		}
	}
}

// Size returns the current entry count. Test-only.
func (c *idemCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.rec)
}

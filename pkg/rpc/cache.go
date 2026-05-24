package rpc

import (
	"sync"
	"time"
)

// idempotencyTTL is the spec-pinned 60s window inside which a repeated
// (verb, idempotency_key) pair returns the cached response without
// re-executing the handler. specs/protocols/rpc-catalog.md §"Idempotency".
const idempotencyTTL = 60 * time.Second

// idempotencyMaxEntries caps memory growth between sweep ticks. A
// bursty client publishing many unique idempotency keys could otherwise
// grow the map unboundedly. When the cap is hit, Store first drops
// every expired entry, then — if still over cap — evicts the entry
// with the soonest expiry. A re-Store of an existing key extends its
// lifetime rather than counting against the cap.
//
// Documented in specs/protocols/rpc-catalog.md §"Idempotency".
const idempotencyMaxEntries = 10000

// idemCache is the server's idempotency cache. Keys are
// (verb, idempotency_key); values are the cached reply envelopes that
// would be re-published byte-for-byte on a repeat request.
//
// In-memory only; a daemon restart drops the cache. The 60s TTL is
// short enough that any in-flight retry storm self-resolves either
// from the cache or by re-execution after the window expires.
type idemCache struct {
	mu     sync.Mutex
	now    func() time.Time
	ttl    time.Duration
	maxRec int
	rec    map[idemKey]idemEntry
}

type idemKey struct {
	verb string
	key  string
}

type idemEntry struct {
	reply     []byte
	expiresAt time.Time
}

// newIdemCache returns a cache that prunes entries older than the TTL
// and caps total memory at max entries. The now closure is injectable
// for tests. max <= 0 falls back to idempotencyMaxEntries.
func newIdemCache(now func() time.Time, ttl time.Duration) *idemCache {
	return newIdemCacheBounded(now, ttl, idempotencyMaxEntries)
}

// newIdemCacheBounded is newIdemCache with an explicit cap. Used by
// tests to exercise eviction behavior at small N.
func newIdemCacheBounded(now func() time.Time, ttl time.Duration, max int) *idemCache {
	if now == nil {
		now = time.Now
	}
	if ttl <= 0 {
		ttl = idempotencyTTL
	}
	if max <= 0 {
		max = idempotencyMaxEntries
	}
	return &idemCache{
		now:    now,
		ttl:    ttl,
		maxRec: max,
		rec:    make(map[idemKey]idemEntry),
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
//
// When the cache would exceed its max-entries cap, Store first sweeps
// expired entries; if still over cap, it evicts the entry with the
// soonest expiry. Re-Store of an existing key replaces its entry in
// place — it does NOT count toward the cap.
func (c *idemCache) Store(verb, key string, reply []byte) {
	if key == "" {
		return
	}
	k := idemKey{verb: verb, key: key}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.rec[k]; !exists && len(c.rec) >= c.maxRec {
		c.evictLocked()
	}
	c.rec[k] = idemEntry{
		reply:     append([]byte(nil), reply...),
		expiresAt: c.now().Add(c.ttl),
	}
}

// evictLocked makes room for one new entry. Caller holds c.mu. First
// tries to drop expired entries (cheap and gives the new entry the
// full TTL window). If the map is still at cap, evicts the single
// entry with the soonest expiry — i.e. the next one that would have
// been removed by Sweep anyway. Documented as "eviction by expiry
// order" in the spec.
func (c *idemCache) evictLocked() {
	now := c.now()
	for k, entry := range c.rec {
		if !now.Before(entry.expiresAt) {
			delete(c.rec, k)
		}
	}
	if len(c.rec) < c.maxRec {
		return
	}
	var (
		victim    idemKey
		victimExp time.Time
		set       bool
	)
	for k, entry := range c.rec {
		if !set || entry.expiresAt.Before(victimExp) {
			victim = k
			victimExp = entry.expiresAt
			set = true
		}
	}
	if set {
		delete(c.rec, victim)
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

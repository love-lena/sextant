package rpc

import (
	"fmt"
	"testing"
	"time"
)

func TestIdemCacheStoreAndLookup(t *testing.T) {
	now := time.Unix(0, 0)
	c := newIdemCache(func() time.Time { return now }, 60*time.Second)
	c.Store("verb", "key-1", []byte("reply"))
	got, ok := c.Lookup("verb", "key-1")
	if !ok {
		t.Fatal("Lookup miss within TTL")
	}
	if string(got) != "reply" {
		t.Fatalf("got %q, want reply", got)
	}
}

func TestIdemCacheExpires(t *testing.T) {
	now := time.Unix(0, 0)
	tick := now
	c := newIdemCache(func() time.Time { return tick }, 10*time.Millisecond)
	c.Store("v", "k", []byte("hi"))
	// Advance past TTL.
	tick = tick.Add(20 * time.Millisecond)
	if _, ok := c.Lookup("v", "k"); ok {
		t.Fatal("Lookup must miss after TTL")
	}
}

func TestIdemCacheRejectsEmptyKey(t *testing.T) {
	c := newIdemCache(time.Now, 60*time.Second)
	c.Store("v", "", []byte("ignored"))
	if c.Size() != 0 {
		t.Fatalf("empty key must not enter cache (size=%d)", c.Size())
	}
	if _, ok := c.Lookup("v", ""); ok {
		t.Fatal("empty-key Lookup must always miss")
	}
}

func TestIdemCacheSweepClearsExpired(t *testing.T) {
	now := time.Unix(0, 0)
	tick := now
	c := newIdemCache(func() time.Time { return tick }, 5*time.Millisecond)
	c.Store("v", "k1", []byte("a"))
	c.Store("v", "k2", []byte("b"))
	if c.Size() != 2 {
		t.Fatalf("Size = %d, want 2", c.Size())
	}
	tick = tick.Add(10 * time.Millisecond)
	c.Sweep()
	if c.Size() != 0 {
		t.Fatalf("Sweep left %d entries, want 0", c.Size())
	}
}

// TestIdemCacheRespectsMaxEntries asserts the cache cap holds — many
// distinct keys never grow the map past the configured bound.
// Eviction is by soonest expiry; with all entries inserted at the
// same logical time the picked victim is implementation-defined, but
// Size must never exceed the cap.
func TestIdemCacheRespectsMaxEntries(t *testing.T) {
	now := time.Unix(0, 0)
	c := newIdemCacheBounded(func() time.Time { return now }, 60*time.Second, 4)
	for i := 0; i < 10; i++ {
		c.Store("v", fmt.Sprintf("k-%d", i), []byte("reply"))
		if got := c.Size(); got > 4 {
			t.Fatalf("after %d stores, Size = %d, want <= 4", i+1, got)
		}
	}
	if c.Size() != 4 {
		t.Fatalf("final Size = %d, want 4", c.Size())
	}
}

// TestIdemCacheReStoreDoesNotCountAgainstCap proves that overwriting
// an existing key doesn't consume a fresh slot — the cap is on
// distinct keys, not on Store calls.
func TestIdemCacheReStoreDoesNotCountAgainstCap(t *testing.T) {
	now := time.Unix(0, 0)
	c := newIdemCacheBounded(func() time.Time { return now }, 60*time.Second, 2)
	c.Store("v", "k1", []byte("a"))
	c.Store("v", "k2", []byte("b"))
	if c.Size() != 2 {
		t.Fatalf("Size before re-store = %d, want 2", c.Size())
	}
	for i := 0; i < 20; i++ {
		c.Store("v", "k1", []byte(fmt.Sprintf("a-%d", i)))
	}
	if c.Size() != 2 {
		t.Fatalf("Size after re-store = %d, want 2 (re-Store must not count against cap)", c.Size())
	}
	if _, ok := c.Lookup("v", "k2"); !ok {
		t.Fatal("k2 was evicted by k1 re-store; re-Store must not evict other entries")
	}
	if got, _ := c.Lookup("v", "k1"); string(got) != "a-19" {
		t.Fatalf("k1 value = %q, want latest a-19", got)
	}
}

// TestIdemCacheEvictsExpiredBeforeLive ensures expired entries get
// dropped first when we hit the cap — a live entry shouldn't be
// evicted while an already-dead one sits in the map.
func TestIdemCacheEvictsExpiredBeforeLive(t *testing.T) {
	now := time.Unix(0, 0)
	tick := now
	c := newIdemCacheBounded(func() time.Time { return tick }, 10*time.Millisecond, 2)
	c.Store("v", "old", []byte("old"))
	// Advance past TTL so "old" is expired.
	tick = tick.Add(20 * time.Millisecond)
	// "fresh1" lands when "old" is already dead — eviction should
	// remove "old" rather than "fresh1". Then fresh2 should fit too.
	c.Store("v", "fresh1", []byte("fresh1"))
	c.Store("v", "fresh2", []byte("fresh2"))
	if c.Size() != 2 {
		t.Fatalf("Size = %d, want 2", c.Size())
	}
	if _, ok := c.Lookup("v", "old"); ok {
		t.Fatal("expired entry survived eviction")
	}
	if _, ok := c.Lookup("v", "fresh1"); !ok {
		t.Fatal("fresh1 was evicted instead of the expired old entry")
	}
	if _, ok := c.Lookup("v", "fresh2"); !ok {
		t.Fatal("fresh2 was evicted instead of the expired old entry")
	}
}

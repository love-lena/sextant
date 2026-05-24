package rpc

import (
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

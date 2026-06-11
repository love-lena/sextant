package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/sextant"
)

func countingNames(calls *atomic.Int64) *nameCache {
	return newNameCache(func(ctx context.Context) ([]sextant.ClientInfo, error) {
		calls.Add(1)
		return []sextant.ClientInfo{{ID: "01A", DisplayName: "alice"}}, nil
	})
}

func TestDisplayNameResolvesAndCaches(t *testing.T) {
	var calls atomic.Int64
	nc := countingNames(&calls)
	ctx := context.Background()

	if got := nc.displayName(ctx, "01A"); got != "alice" {
		t.Errorf("displayName(01A) = %q, want alice", got)
	}
	if got := nc.displayName(ctx, "01A"); got != "alice" {
		t.Errorf("cached displayName(01A) = %q, want alice", got)
	}
	if calls.Load() != 1 {
		t.Errorf("list called %d times for a cached id, want 1", calls.Load())
	}
}

func TestDisplayNameRefreshIsRateLimited(t *testing.T) {
	var calls atomic.Int64
	nc := countingNames(&calls)
	ctx := context.Background()

	// First miss refreshes once; repeated misses inside the interval (a
	// 100-frame read by an unknown author) must not re-list per frame.
	for range 50 {
		if got := nc.displayName(ctx, "01GONE"); got != "01GONE" {
			t.Fatalf("unknown id = %q, want raw-id fallback", got)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("list called %d times inside the rate-limit window, want 1", calls.Load())
	}

	// Past the interval a miss may refresh again.
	nc.mu.Lock()
	nc.lastRefresh = time.Now().Add(-time.Minute)
	nc.mu.Unlock()
	nc.displayName(ctx, "01GONE")
	if calls.Load() != 2 {
		t.Errorf("list called %d times after the window, want 2", calls.Load())
	}
}

func TestDisplayNameCachedNeverBlocksAndHeals(t *testing.T) {
	var calls atomic.Int64
	nc := countingNames(&calls)

	// Cold cache: the raw id comes back immediately; the refresh it kicked
	// fills the cache for the frames after it.
	if got := nc.displayNameCached("01A"); got != "01A" {
		t.Errorf("cold displayNameCached = %q, want the raw id", got)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := nc.displayNameCached("01A"); got == "alice" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("async refresh never named 01A")
}

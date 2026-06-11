package main

import (
	"context"
	"testing"

	"github.com/love-lena/sextant/pkg/sextant"
)

func TestNameCacheResolvesCachesAndFallsBack(t *testing.T) {
	calls := 0
	nc := newNameCache(func(ctx context.Context) ([]sextant.ClientInfo, error) {
		calls++
		return []sextant.ClientInfo{{ID: "01A", DisplayName: "alice"}}, nil
	})
	ctx := context.Background()

	if got := nc.displayName(ctx, "01A"); got != "alice" {
		t.Errorf("displayName(01A) = %q, want alice", got)
	}
	if got := nc.displayName(ctx, "01A"); got != "alice" {
		t.Errorf("cached displayName(01A) = %q, want alice", got)
	}
	if calls != 1 {
		t.Errorf("list called %d times for a cached id, want 1", calls)
	}

	// Unknown id: one refresh, then fall back to the raw id without error.
	if got := nc.displayName(ctx, "01B"); got != "01B" {
		t.Errorf("displayName(01B) = %q, want raw id fallback", got)
	}
	if calls != 2 {
		t.Errorf("list called %d times after a miss, want 2", calls)
	}
}

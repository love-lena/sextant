package main

import (
	"context"
	"sync"
	"time"

	"github.com/love-lena/sextant/pkg/sextant"
)

// defaultRefreshMinInterval rate-limits directory refreshes: a read of many
// frames by an unknown (e.g. retired) author triggers one re-list, not one
// per frame.
const defaultRefreshMinInterval = 2 * time.Second

// nameCache resolves author ULIDs to display names via the clients directory
// (dogfood learning #5: the join is this server's job, not the agent's). An
// id the directory doesn't know falls back to the raw id.
type nameCache struct {
	minInterval time.Duration

	mu          sync.Mutex
	byID        map[string]string
	lastRefresh time.Time
	refreshing  bool
	list        func(ctx context.Context) ([]sextant.ClientInfo, error)
}

func newNameCache(list func(ctx context.Context) ([]sextant.ClientInfo, error)) *nameCache {
	return &nameCache{minInterval: defaultRefreshMinInterval, byID: map[string]string{}, list: list}
}

// displayName resolves id, refreshing the directory on a miss (rate-limited).
// It may call the bus; do not use it on the SDK delivery path — that's what
// displayNameCached is for.
func (n *nameCache) displayName(ctx context.Context, id string) string {
	if name, ok := n.lookup(id); ok {
		return name
	}
	n.refresh(ctx)
	if name, ok := n.lookup(id); ok {
		return name
	}
	return id
}

// displayNameCached never blocks: the cached name, or the raw id while a
// background refresh fills the cache for the frames after it. Safe on the
// SDK delivery goroutine, where a directory call would stall delivery (and
// the SDK warns against calling the client from its own handlers).
func (n *nameCache) displayNameCached(id string) string {
	if name, ok := n.lookup(id); ok {
		return name
	}
	go n.refresh(context.Background())
	return id
}

func (n *nameCache) lookup(id string) (string, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	name, ok := n.byID[id]
	return name, ok
}

// refresh re-lists the directory unless one ran within refreshMinInterval or
// is already in flight. The directory call runs outside the lock, so lookups
// never wait on the bus. The stamp advances on errors too — a down bus is
// retried at the rate limit, not hammered per frame.
func (n *nameCache) refresh(ctx context.Context) {
	n.mu.Lock()
	if n.refreshing || time.Since(n.lastRefresh) < n.minInterval {
		n.mu.Unlock()
		return
	}
	n.refreshing = true
	n.mu.Unlock()

	clients, err := n.list(ctx)

	n.mu.Lock()
	n.refreshing = false
	n.lastRefresh = time.Now()
	if err == nil {
		for _, c := range clients {
			n.byID[c.ID] = c.DisplayName
		}
	}
	n.mu.Unlock()
}

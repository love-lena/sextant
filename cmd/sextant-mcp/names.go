package main

import (
	"context"
	"sync"

	"github.com/love-lena/sextant/pkg/sextant"
)

// nameCache resolves author ULIDs to display names via the clients directory
// (dogfood learning #5: the join is this server's job, not the agent's). On a
// miss it refreshes once; an id the directory still doesn't know falls back
// to the raw id.
type nameCache struct {
	mu   sync.Mutex
	byID map[string]string
	list func(ctx context.Context) ([]sextant.ClientInfo, error)
}

func newNameCache(list func(ctx context.Context) ([]sextant.ClientInfo, error)) *nameCache {
	return &nameCache{byID: map[string]string{}, list: list}
}

func (n *nameCache) displayName(ctx context.Context, id string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if name, ok := n.byID[id]; ok {
		return name
	}
	if clients, err := n.list(ctx); err == nil {
		for _, c := range clients {
			n.byID[c.ID] = c.DisplayName
		}
	}
	if name, ok := n.byID[id]; ok {
		return name
	}
	return id
}

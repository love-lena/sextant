package busfeed

// DroppedCount returns the coalesced drop count not yet carried in-band by an
// enqueued item. Tests use it to synchronize with the asynchronous SDK delivery:
// waiting for it to reach the expected overflow proves the whole flood has been
// delivered (buffer full, excess dropped); waiting for it to return to zero
// proves a post-gap event has been enqueued carrying the gap.
func (f *Feed) DroppedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dropped
}

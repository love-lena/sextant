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

// BufferedCount returns how many events are queued between the SDK handler and
// the pump. Tests use it to synchronize with the asynchronous SDK delivery:
// waiting for it to reach a known count proves the published events have been
// enqueued before the test injects an error or drains the pump.
func (f *Feed) BufferedCount() int { return len(f.events) }

// InjectError drives the feed's SDK OnError handler directly, exactly as the
// NATS callback goroutine would on a resume failure, so a test can exercise the
// fatal/deferred routing without engineering a real reconnect-time failure.
func (f *Feed) InjectError(err error) { f.onError(err) }

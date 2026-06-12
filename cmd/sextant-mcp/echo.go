package main

import "sync"

// echoSetSize is the number of recently-published frame ids this server
// retains to suppress self-echo. 256 slots cover a sustained burst of ~256
// publishes before the oldest ids evict — far beyond any realistic session
// cadence. The ring is per-process, so it is tightly scoped and never
// communicated externally.
const echoSetSize = 256

// selfEchoSet is a bounded ring of recently-published frame ids. When
// message_publish succeeds, the caller records the bus-stamped frame id here;
// when the subscription delivery path receives a frame, it checks here before
// emitting a channel event. A frame whose id is in the set is the publisher's
// own echo and is dropped silently. The set is id-based, not author-based: a
// resumed or co-identity session that publishes under the same client id but a
// different process (i.e. a different selfEchoSet) still sees those frames.
type selfEchoSet struct {
	mu   sync.Mutex
	ring [echoSetSize]string
	head int // index of the slot to overwrite next
	seen map[string]struct{}
}

func newSelfEchoSet() *selfEchoSet {
	return &selfEchoSet{seen: make(map[string]struct{}, echoSetSize)}
}

// record adds id to the set, evicting the oldest entry if the ring is full.
func (s *selfEchoSet) record(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Evict the slot we are about to overwrite.
	if old := s.ring[s.head]; old != "" {
		delete(s.seen, old)
	}
	s.ring[s.head] = id
	s.seen[id] = struct{}{}
	s.head = (s.head + 1) % echoSetSize
}

// contains reports whether id is a recently-published frame id.
func (s *selfEchoSet) contains(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	_, ok := s.seen[id]
	s.mu.Unlock()
	return ok
}

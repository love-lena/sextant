package violet

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/pkg/wire"
)

// fakeBus is an in-memory busClient for the orchestrator tests. It records
// subscriptions (so a test can deliver frames to the handlers and assert the
// scoped set), captures publishes (so a test can time + read replies), and
// serves a small artifact store (so the deep pass's gather + home write run for
// real). It drives the REAL role goroutines — the concurrency is genuine, only
// the bus + model are faked.
//
// It also implements FetchMessages for the AC8 replay path: retained DMs are
// stored in dmHistory so tests can inject offline-gap messages.
type fakeBus struct {
	self     string
	operator string

	mu        sync.Mutex
	subs      map[string]func(Message) // subject → handler
	artifacts map[string]artifactValue
	rev       uint64

	// dmHistory holds retained DM frames for the AC8 offline-gap replay test.
	// Each entry is a fetchedFrame; the slice is ordered oldest-first.
	// Guarded by mu.
	dmHistory []fetchedFrame
	dmSeq     uint64 // next sequence number to assign to a new DM

	publishMu sync.Mutex
	publishes []publishedFrame
	pubCond   *sync.Cond
}

type publishedFrame struct {
	subject string
	record  json.RawMessage
}

func newFakeBus(self, operator string) *fakeBus {
	b := &fakeBus{
		self:      self,
		operator:  operator,
		subs:      map[string]func(Message){},
		artifacts: map[string]artifactValue{},
	}
	b.pubCond = sync.NewCond(&b.publishMu)
	return b
}

func (b *fakeBus) ID() string        { return b.self }
func (b *fakeBus) Principal() string { return b.operator }

func (b *fakeBus) PublishMsg(_ context.Context, subject string, record json.RawMessage) (publishResult, error) {
	b.publishMu.Lock()
	b.publishes = append(b.publishes, publishedFrame{subject: subject, record: record})
	b.pubCond.Broadcast()
	b.publishMu.Unlock()
	return publishResult{ID: "pub"}, nil
}

func (b *fakeBus) Subscribe(_ context.Context, subject string, h func(Message), _ ...subOpt) (stopper, error) {
	b.mu.Lock()
	b.subs[subject] = h
	b.mu.Unlock()
	return fakeSub{}, nil
}

func (b *fakeBus) GetArtifact(_ context.Context, name string) (artifactValue, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	art, ok := b.artifacts[name]
	if !ok {
		return artifactValue{}, errNotFound
	}
	return art, nil
}

func (b *fakeBus) CreateArtifact(_ context.Context, name string, record wire.Lexicon) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.artifacts[name]; ok {
		return 0, errExists
	}
	b.rev++
	b.artifacts[name] = artifactValue{Name: name, Record: json.RawMessage(record), Revision: b.rev}
	return b.rev, nil
}

func (b *fakeBus) UpdateArtifact(_ context.Context, name string, record wire.Lexicon, expectedRev uint64) (uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	art, ok := b.artifacts[name]
	if !ok {
		return 0, errNotFound
	}
	if art.Revision != expectedRev {
		return 0, errStaleCAS
	}
	b.rev++
	b.artifacts[name] = artifactValue{Name: name, Record: json.RawMessage(record), Revision: b.rev}
	return b.rev, nil
}

func (b *fakeBus) ListArtifacts(context.Context) ([]artifactInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]artifactInfo, 0, len(b.artifacts))
	for _, a := range b.artifacts {
		out = append(out, artifactInfo{Name: a.Name, Revision: a.Revision})
	}
	return out, nil
}

// FetchMessages implements the AC8 offline-gap replay pull path. It returns
// frames from dmHistory (the operator DMs injected via retainDM) starting from
// `since`, up to `limit` frames. The subject is ignored — a fake bus has only
// one DM history (the test only calls this for the DM subject). Returns the
// frames and the next cursor (since + len(frames)).
func (b *fakeBus) FetchMessages(_ context.Context, _ string, since uint64, limit int) ([]fetchedFrame, uint64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []fetchedFrame
	for _, f := range b.dmHistory {
		if f.Sequence <= since {
			continue // already seen (since is "already answered up to this seq")
		}
		out = append(out, f)
		if len(out) >= limit {
			break
		}
	}
	next := since
	if len(out) > 0 {
		next = out[len(out)-1].Sequence
	}
	return out, next, nil
}

// retainDM injects an operator DM into the bus's retained history (as if the
// bus stored it while violet was offline). Used by AC8 tests to simulate an
// offline-gap message.
func (b *fakeBus) retainDM(record json.RawMessage) fetchedFrame {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dmSeq++
	f := fetchedFrame{
		Author:   b.operator,
		Sequence: b.dmSeq,
		Record:   record,
	}
	b.dmHistory = append(b.dmHistory, f)
	return f
}

// retainDMFromStranger injects a DM from a NON-operator author into history.
// Used to assert that the replay never answers cross-client messages (criterion 1).
func (b *fakeBus) retainDMFromStranger(authorID string, record json.RawMessage) fetchedFrame {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dmSeq++
	f := fetchedFrame{
		Author:   authorID,
		Sequence: b.dmSeq,
		Record:   record,
	}
	b.dmHistory = append(b.dmHistory, f)
	return f
}

// deliver pushes a frame to the matching subscription handler (exact subject, or
// a wildcard prefix match for the artifact.> subscription), as the SDK relay
// would. It runs the handler synchronously on its own goroutine, mirroring the
// SDK delivering on its own goroutine — so a handler that enqueues onto a
// channel returns immediately and the test's deliver loop is not blocked.
func (b *fakeBus) deliver(subject, author string, record json.RawMessage) {
	b.mu.Lock()
	var h func(Message)
	if exact, ok := b.subs[subject]; ok {
		h = exact
	} else {
		// Wildcard match (e.g. msg.topic.artifact.> covers msg.topic.artifact.foo).
		for sub, handler := range b.subs {
			if wildcardMatch(sub, subject) {
				h = handler
				break
			}
		}
	}
	b.mu.Unlock()
	if h == nil {
		return
	}
	go h(Message{Author: author, Subject: subject, Record: record})
}

// wildcardMatch reports whether a NATS-style ">" wildcard subject matches a
// concrete subject (only the trailing ">" form violet uses needs support).
func wildcardMatch(pattern, subject string) bool {
	if len(pattern) >= 2 && pattern[len(pattern)-1] == '>' {
		prefix := pattern[:len(pattern)-1] // includes the trailing "."
		return len(subject) >= len(prefix) && subject[:len(prefix)] == prefix
	}
	return pattern == subject
}

func (b *fakeBus) subjects() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.subs))
	for s := range b.subs {
		out = append(out, s)
	}
	return out
}

func (b *fakeBus) waitSubscribed(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		got := len(b.subs)
		b.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("not subscribed to %d subjects within 2s (got %d)", n, len(b.subjects()))
}

// awaitPublish blocks until a frame is published on subject (returning its
// record) or the deadline elapses. It scans frames published since the last
// awaited index so each reply is consumed once.
func (b *fakeBus) awaitPublish(subject string, within time.Duration) (json.RawMessage, bool) {
	deadline := time.Now().Add(within)
	b.publishMu.Lock()
	defer b.publishMu.Unlock()
	scanned := 0
	for {
		for ; scanned < len(b.publishes); scanned++ {
			if b.publishes[scanned].subject == subject {
				return b.publishes[scanned].record, true
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, false
		}
		// Wait with a timeout: a watchdog goroutine broadcasts at the deadline so
		// Cond.Wait returns even if no publish arrives.
		timer := time.AfterFunc(remaining, func() {
			b.publishMu.Lock()
			b.pubCond.Broadcast()
			b.publishMu.Unlock()
		})
		b.pubCond.Wait()
		timer.Stop()
		if time.Now().After(deadline) && scanned >= len(b.publishes) {
			return nil, false
		}
	}
}

type fakeSub struct{}

func (fakeSub) Stop() {}

type busErr string

func (e busErr) Error() string { return string(e) }

const (
	errNotFound busErr = "violet-test: artifact not found"
	errExists   busErr = "violet-test: artifact exists"
	errStaleCAS busErr = "violet-test: stale CAS"
)

package dashapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/wire"
)

// fakeBus is a test double for dashapi.Bus: canned directory/artifact/message
// data and a controllable subscription so a test can push live frames. It is the
// second implementation of the Bus interface (the production one is
// *sextant.Client), so the handlers are exercised without a real bus.
type fakeBus struct {
	id        string
	display   string
	principal string

	clients   []sextant.ClientInfo
	artifacts []sextant.ArtifactInfo
	artifact  map[string]sextant.Artifact
	frames    []wire.Frame
	nextCur   uint64

	clientsErr     error
	fetchErr       error
	artifactErr    error
	publishErr     error
	subErr         error
	failUpdates    int // when >0, UpdateArtifact returns errConflict and decrements (exercises the CAS retry)
	failGoalUpdate int // like failUpdates but only for goal.* writes — targets the closed-loop's CAS retry without tripping the verdict write

	mu             sync.Mutex
	published      []publishedMsg
	subs           []*fakeSub
	lastFetchSubj  string
	lastFetchSince uint64
	lastFetchLimit int
	lastSubSubject string
}

type publishedMsg struct {
	subject string
	record  json.RawMessage
}

func (f *fakeBus) ID() string          { return f.id }
func (f *fakeBus) DisplayName() string { return f.display }
func (f *fakeBus) Principal() string   { return f.principal }

func (f *fakeBus) ListClients(context.Context) ([]sextant.ClientInfo, error) {
	return f.clients, f.clientsErr
}

func (f *fakeBus) FetchMessages(_ context.Context, subject string, since uint64, limit int) ([]wire.Frame, uint64, error) {
	f.mu.Lock()
	f.lastFetchSubj, f.lastFetchSince, f.lastFetchLimit = subject, since, limit
	f.mu.Unlock()
	if f.fetchErr != nil {
		return nil, 0, f.fetchErr
	}
	return f.frames, f.nextCur, nil
}

func (f *fakeBus) ListArtifacts(context.Context) ([]sextant.ArtifactInfo, error) {
	return f.artifacts, f.artifactErr
}

func (f *fakeBus) GetArtifact(_ context.Context, name string) (sextant.Artifact, error) {
	if f.artifactErr != nil {
		return sextant.Artifact{}, f.artifactErr
	}
	a, ok := f.artifact[name]
	if !ok {
		return sextant.Artifact{}, errNotFound
	}
	return a, nil
}

func (f *fakeBus) UpdateArtifact(_ context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error) {
	if f.artifactErr != nil {
		return 0, f.artifactErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasPrefix(name, "goal.") && f.failGoalUpdate > 0 {
		f.failGoalUpdate--
		return 0, errConflict
	}
	if f.failUpdates > 0 {
		f.failUpdates--
		return 0, errConflict
	}
	a, ok := f.artifact[name]
	if !ok {
		return 0, errNotFound
	}
	if a.Revision != expectedRev {
		return 0, errConflict
	}
	a.Record = wire.Lexicon(record)
	a.Revision++
	f.artifact[name] = a
	return a.Revision, nil
}

func (f *fakeBus) Publish(_ context.Context, subject string, record json.RawMessage) error {
	if f.publishErr != nil {
		return f.publishErr
	}
	f.mu.Lock()
	f.published = append(f.published, publishedMsg{subject: subject, record: record})
	f.mu.Unlock()
	return nil
}

func (f *fakeBus) Subscribe(_ context.Context, subject string, h sextant.Handler, _ ...sextant.SubOption) (sextant.Subscription, error) {
	if f.subErr != nil {
		return nil, f.subErr
	}
	s := &fakeSub{subject: subject, handler: h}
	f.mu.Lock()
	f.lastSubSubject = subject
	f.subs = append(f.subs, s)
	f.mu.Unlock()
	return s, nil
}

// push delivers m to every active subscription's handler — the test stand-in for
// the bus relaying a live frame.
func (f *fakeBus) push(m sextant.Message) {
	f.mu.Lock()
	subs := append([]*fakeSub(nil), f.subs...)
	f.mu.Unlock()
	for _, s := range subs {
		s.handler(m)
	}
}

func (f *fakeBus) publishedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.published)
}

func (f *fakeBus) activeSubs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.subs {
		if !s.isStopped() {
			n++
		}
	}
	return n
}

type fakeSub struct {
	subject string
	handler sextant.Handler
	mu      sync.Mutex
	stopped bool
}

func (s *fakeSub) Stop() {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
}

func (s *fakeSub) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// errNotFound is the fake's "no such artifact" — its message mirrors what the
// bus returns so a handler test can assert the 404 path without a real bus.
var errNotFound = &fakeError{"artifact not found"}

// errConflict is the fake's compare-and-set failure — a stand-in for the bus
// rejecting an UpdateArtifact whose expectedRev no longer matches.
var errConflict = &fakeError{"revision conflict"}

type fakeError struct{ msg string }

func (e *fakeError) Error() string { return e.msg }

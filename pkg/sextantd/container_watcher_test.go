package sextantd

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// fakeEvents satisfies EventSource for container_watcher tests.
type fakeEvents struct {
	events <-chan containermgr.Event
	errs   <-chan error
}

func (f *fakeEvents) Events(_ context.Context, _ containermgr.EventsFilter) (<-chan containermgr.Event, <-chan error) {
	return f.events, f.errs
}

// newFakeEventSource returns the source and the write ends of both channels.
func newFakeEventSource() (*fakeEvents, chan<- containermgr.Event, chan<- error) {
	evCh := make(chan containermgr.Event, 8)
	errCh := make(chan error, 1)
	return &fakeEvents{events: evCh, errs: errCh}, evCh, errCh
}

// makeDieEvent builds a die event with the standard sextant labels.
func makeDieEvent(agentID, incID uuid.UUID) containermgr.Event {
	return containermgr.Event{
		ContainerID: "container-" + incID.String()[:8],
		Action:      "die",
		Time:        time.Now(),
		Labels: map[string]string{
			handlers.LabelAgentUUID:     agentID.String(),
			handlers.LabelIncarnationID: incID.String(),
		},
	}
}

// publishRecorder is the test-side capture for ContainerWatcher's publish
// callback. The watcher invokes publishFn from a debounce goroutine that
// outlives Run() — the recorder serializes writes with a mutex and exposes
// a deterministic Wait/Count surface for the test.
type publishRecorder struct {
	mu       sync.Mutex
	envs     []sextantproto.Envelope
	signaled chan struct{}
}

func newPublishRecorder() *publishRecorder {
	return &publishRecorder{signaled: make(chan struct{}, 8)}
}

func (p *publishRecorder) publish(_ context.Context, env sextantproto.Envelope) error {
	p.mu.Lock()
	p.envs = append(p.envs, env)
	p.mu.Unlock()
	select {
	case p.signaled <- struct{}{}:
	default:
	}
	return nil
}

func (p *publishRecorder) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.envs)
}

func (p *publishRecorder) snapshot() []sextantproto.Envelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]sextantproto.Envelope, len(p.envs))
	copy(out, p.envs)
	return out
}

// waitPublished blocks until n publishes have been recorded or the timeout
// elapses. Returns true if n was reached.
func (p *publishRecorder) waitPublished(t *testing.T, n int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	for p.count() < n {
		select {
		case <-p.signaled:
		case <-deadline:
			return false
		}
	}
	return true
}

// TestContainerWatcherPublishesLostAfterDebounce emits a die event and
// asserts exactly one lost envelope is published after the debounce
// window with Source = container_watcher.
func TestContainerWatcherPublishesLostAfterDebounce(t *testing.T) {
	agentID := uuid.New()
	incID := uuid.New()

	src, evCh, errCh := newFakeEventSource()
	defer close(errCh)

	rec := newPublishRecorder()
	const debounce = 20 * time.Millisecond
	w := NewContainerWatcher(src, rec.publish, WithDebounce(debounce))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	evCh <- makeDieEvent(agentID, incID)

	// Wait deterministically for the publish to land.
	if !rec.waitPublished(t, 1, 2*time.Second) {
		t.Fatalf("publish did not arrive within 2s; got %d", rec.count())
	}

	close(evCh)
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}

	envs := rec.snapshot()
	if len(envs) != 1 {
		t.Fatalf("published %d envelopes, want 1", len(envs))
	}

	var payload sextantproto.LifecyclePayload
	if err := json.Unmarshal(envs[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Transition != sextantproto.LifecycleLostEvent {
		t.Errorf("Transition = %q, want %q", payload.Transition, sextantproto.LifecycleLostEvent)
	}
	if payload.AgentUUID != agentID {
		t.Errorf("AgentUUID = %s, want %s", payload.AgentUUID, agentID)
	}
	if payload.IncarnationID != incID {
		t.Errorf("IncarnationID = %s, want %s", payload.IncarnationID, incID)
	}
	if payload.Source != sextantproto.LifecycleSourceContainerWatcher {
		t.Errorf("Source = %q, want %q", payload.Source, sextantproto.LifecycleSourceContainerWatcher)
	}
}

// TestContainerWatcherCancelsDebounceOnSidecarTerminal emits a die event
// then calls OnSidecarLifecycle with a terminal transition (ended) before
// the debounce fires. Asserts ZERO publishes.
func TestContainerWatcherCancelsDebounceOnSidecarTerminal(t *testing.T) {
	agentID := uuid.New()
	incID := uuid.New()

	src, evCh, errCh := newFakeEventSource()
	defer close(errCh)

	rec := newPublishRecorder()
	const debounce = 100 * time.Millisecond
	w := NewContainerWatcher(src, rec.publish, WithDebounce(debounce))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	evCh <- makeDieEvent(agentID, incID)

	// Cancel the debounce by signalling a terminal sidecar lifecycle
	// before the window expires.
	time.Sleep(10 * time.Millisecond)
	w.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID:     agentID,
		IncarnationID: incID,
		Transition:    sextantproto.LifecycleEnded,
	})

	// Wait past the full debounce window to verify nothing was published.
	time.Sleep(debounce * 2)
	close(evCh)

	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}

	if got := rec.count(); got != 0 {
		t.Errorf("published %d envelopes, want 0 (debounce should have been cancelled)", got)
	}
}

// TestContainerWatcherIgnoresNonTerminalSidecarSignal emits a die event
// then calls OnSidecarLifecycle with a non-terminal transition
// (turn_ended). Asserts exactly ONE publish — the non-terminal signal
// must not cancel the debounce timer.
func TestContainerWatcherIgnoresNonTerminalSidecarSignal(t *testing.T) {
	agentID := uuid.New()
	incID := uuid.New()

	src, evCh, errCh := newFakeEventSource()
	defer close(errCh)

	rec := newPublishRecorder()
	const debounce = 20 * time.Millisecond
	w := NewContainerWatcher(src, rec.publish, WithDebounce(debounce))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	evCh <- makeDieEvent(agentID, incID)

	// Non-terminal transition arrives during the debounce window. Must
	// NOT cancel the timer.
	time.Sleep(5 * time.Millisecond)
	w.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID:     agentID,
		IncarnationID: incID,
		Transition:    sextantproto.LifecycleTurnEnded,
	})

	// Wait deterministically for the (uncancelled) publish.
	if !rec.waitPublished(t, 1, 2*time.Second) {
		t.Fatalf("publish did not arrive within 2s; got %d", rec.count())
	}

	close(evCh)
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}

	if got := rec.count(); got != 1 {
		t.Fatalf("published %d envelopes, want 1 (non-terminal signal must not cancel timer)", got)
	}
}

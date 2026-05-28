package sextantd

import (
	"context"
	"encoding/json"
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
			handlers.LabelAgentUUID:    agentID.String(),
			handlers.LabelIncarnationID: incID.String(),
		},
	}
}

// TestContainerWatcherPublishesLostAfterDebounce emits a die event and
// asserts exactly one lost envelope is published after the debounce
// window with Source = container_watcher.
func TestContainerWatcherPublishesLostAfterDebounce(t *testing.T) {
	agentID := uuid.New()
	incID := uuid.New()

	src, evCh, errCh := newFakeEventSource()
	defer close(errCh)

	var published []sextantproto.Envelope
	publishFn := func(_ context.Context, env sextantproto.Envelope) error {
		published = append(published, env)
		return nil
	}

	const debounce = 20 * time.Millisecond
	w := NewContainerWatcher(src, publishFn, WithDebounce(debounce))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	// Send the die event and then close the events channel so Run exits.
	evCh <- makeDieEvent(agentID, incID)
	// Wait for debounce to fire before closing the channel.
	time.Sleep(debounce * 3)
	close(evCh)

	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}

	if len(published) != 1 {
		t.Fatalf("published %d envelopes, want 1", len(published))
	}

	var payload sextantproto.LifecyclePayload
	if err := json.Unmarshal(published[0].Payload, &payload); err != nil {
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

	var published []sextantproto.Envelope
	publishFn := func(_ context.Context, env sextantproto.Envelope) error {
		published = append(published, env)
		return nil
	}

	const debounce = 100 * time.Millisecond
	w := NewContainerWatcher(src, publishFn, WithDebounce(debounce))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	// Emit the die event.
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

	if len(published) != 0 {
		t.Errorf("published %d envelopes, want 0 (debounce should have been cancelled)", len(published))
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

	var published []sextantproto.Envelope
	publishFn := func(_ context.Context, env sextantproto.Envelope) error {
		published = append(published, env)
		return nil
	}

	const debounce = 20 * time.Millisecond
	w := NewContainerWatcher(src, publishFn, WithDebounce(debounce))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- w.Run(ctx) }()

	// Emit the die event.
	evCh <- makeDieEvent(agentID, incID)

	// Notify with a non-terminal transition (turn_ended) — should NOT
	// cancel the debounce.
	time.Sleep(5 * time.Millisecond)
	w.OnSidecarLifecycle(sextantproto.LifecyclePayload{
		AgentUUID:     agentID,
		IncarnationID: incID,
		Transition:    sextantproto.LifecycleTurnEnded,
	})

	// Wait past the debounce window.
	time.Sleep(debounce * 3)
	close(evCh)

	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return")
	}

	if len(published) != 1 {
		t.Fatalf("published %d envelopes, want 1 (non-terminal signal must not cancel timer)", len(published))
	}
}

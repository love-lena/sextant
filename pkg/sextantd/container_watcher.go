package sextantd

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant/pkg/containermgr"
	"github.com/love-lena/sextant/pkg/rpc/handlers"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// EventSource is the narrow surface ContainerWatcher needs on
// *containermgr.Manager. Tests substitute a fake.
type EventSource interface {
	Events(ctx context.Context, f containermgr.EventsFilter) (<-chan containermgr.Event, <-chan error)
}

// pendingEntry tracks an in-flight debounce timer for one incarnation.
type pendingEntry struct {
	cancel context.CancelFunc
	gen    uint64 // monotonically increasing; guards against stale goroutine cleanup
}

// ContainerWatcher subscribes to container `die` events for labeled sextant
// sidecars and publishes synthetic transition=lost envelopes after a
// debounce window, unless the sidecar publishes a terminal lifecycle
// (ended / crashed) for the same incarnation in the window.
type ContainerWatcher struct {
	src      EventSource
	publish  func(context.Context, sextantproto.Envelope) error
	debounce time.Duration

	mu      sync.Mutex
	pending map[uuid.UUID]pendingEntry // keyed by incarnation_id
	nextGen uint64
}

// ContainerWatcherOption is a functional option for NewContainerWatcher.
type ContainerWatcherOption func(*ContainerWatcher)

// WithDebounce sets the debounce window. Default 5s.
func WithDebounce(d time.Duration) ContainerWatcherOption {
	return func(w *ContainerWatcher) { w.debounce = d }
}

// NewContainerWatcher constructs a ContainerWatcher. src must not be nil;
// publish is the function called when a lost envelope should be delivered.
func NewContainerWatcher(
	src EventSource,
	publish func(context.Context, sextantproto.Envelope) error,
	opts ...ContainerWatcherOption,
) *ContainerWatcher {
	w := &ContainerWatcher{
		src:      src,
		publish:  publish,
		debounce: 5 * time.Second,
		pending:  make(map[uuid.UUID]pendingEntry),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Run subscribes to docker container `die` events for any container
// carrying the sextant.agent_uuid label and blocks until ctx is done,
// the error channel fires, or the event channel is closed.
//
// Call Run once in a dedicated goroutine; it is not safe to call Run
// multiple times concurrently on the same ContainerWatcher.
func (w *ContainerWatcher) Run(ctx context.Context) error {
	events, errs := w.src.Events(ctx, containermgr.EventsFilter{
		Labels: map[string]string{handlers.LabelAgentUUID: ""},
		Events: []string{"die"},
	})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			if err != nil {
				log.Printf("sextantd: container watcher: events stream: %v", err)
			}
			return err
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			w.handleDie(ctx, ev)
		}
	}
}

// OnSidecarLifecycle is called by the daemon's lifecycle subscriber when a
// sidecar-driven envelope is observed. Terminal transitions (ended,
// crashed) cancel any pending `lost` publish for the same incarnation.
// Safe to call concurrently with handleDie and other OnSidecarLifecycle
// calls.
func (w *ContainerWatcher) OnSidecarLifecycle(p sextantproto.LifecyclePayload) {
	if p.Transition != sextantproto.LifecycleEnded &&
		p.Transition != sextantproto.LifecycleCrashedEvent {
		return
	}
	if p.IncarnationID == uuid.Nil {
		return
	}

	w.mu.Lock()
	entry, ok := w.pending[p.IncarnationID]
	if ok {
		delete(w.pending, p.IncarnationID)
	}
	w.mu.Unlock()

	if ok {
		entry.cancel()
	}
}

// handleDie starts a debounce goroutine for the container die event ev.
// If a previous goroutine is already pending for the same incarnation it
// is cancelled first so only one lost envelope can ever be published per
// incarnation.
func (w *ContainerWatcher) handleDie(ctx context.Context, ev containermgr.Event) {
	agentStr := ev.Labels[handlers.LabelAgentUUID]
	incStr := ev.Labels[handlers.LabelIncarnationID]
	if agentStr == "" || incStr == "" {
		return
	}
	agentID, err := uuid.Parse(agentStr)
	if err != nil {
		return
	}
	incID, err := uuid.Parse(incStr)
	if err != nil {
		return
	}

	timerCtx, cancel := context.WithCancel(ctx)

	w.mu.Lock()
	// Cancel any previous pending timer for this incarnation (duplicate
	// die event or a rapid container restart sharing the same incarnation_id).
	if prev, exists := w.pending[incID]; exists {
		prev.cancel()
	}
	w.nextGen++
	myGen := w.nextGen
	w.pending[incID] = pendingEntry{cancel: cancel, gen: myGen}
	w.mu.Unlock()

	go func() {
		defer func() {
			// Only remove from the map if we are still the current entry.
			// A later die event for the same incarnation may have already
			// replaced us with a higher gen.
			w.mu.Lock()
			if cur, ok := w.pending[incID]; ok && cur.gen == myGen {
				delete(w.pending, incID)
			}
			w.mu.Unlock()
			cancel() // always release the context resources
		}()

		select {
		case <-timerCtx.Done():
			// Cancelled by OnSidecarLifecycle or a duplicate die; do not publish.
			return
		case <-time.After(w.debounce):
		}

		env, err := buildLostEnvelope(
			agentID, incID,
			sextantproto.LifecycleSourceContainerWatcher,
			"container died without sidecar lifecycle publish",
		)
		if err != nil {
			log.Printf("sextantd: container watcher: build envelope: %v", err)
			return
		}
		if err := w.publish(ctx, env); err != nil {
			log.Printf("sextantd: container watcher: publish: %v", err)
		}
	}()
}

package sextantd

import (
	"sync"

	"github.com/google/uuid"
)

// workQueue is a keyed, de-duplicating, single-item-per-key work queue
// (RFC §5.1: "keyed, de-duplicating, single-in-flight-per-agent"). A key
// enqueued while already queued (or in flight) collapses to one pending
// entry — events are hints, so coalescing them is correct and is what
// makes a level reconcile cheap under a storm of die/heartbeat hints.
//
// It is the controller-runtime workqueue shape, minus the rate-limiter
// (the reconciler's own backoff path owns retry timing; the periodic
// sweep owns routine re-checks — RFC §5.1 keeps those two distinct).
type workQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	queue    []uuid.UUID
	queued   map[uuid.UUID]struct{} // pending-or-dirty set (de-dup)
	dirty    map[uuid.UUID]struct{} // re-enqueued while in flight
	inflight map[uuid.UUID]struct{} // currently being processed
	shutdown bool
}

func newWorkQueue() *workQueue {
	q := &workQueue{
		queued:   make(map[uuid.UUID]struct{}),
		dirty:    make(map[uuid.UUID]struct{}),
		inflight: make(map[uuid.UUID]struct{}),
	}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// Enqueue adds id to the queue. A no-op when id is already pending. When
// id is in flight, it is marked dirty so it re-queues once the current
// pass finishes (so a hint that lands mid-reconcile is not lost).
func (q *workQueue) Enqueue(id uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.shutdown {
		return
	}
	if _, ok := q.inflight[id]; ok {
		q.dirty[id] = struct{}{}
		return
	}
	if _, ok := q.queued[id]; ok {
		return
	}
	q.queued[id] = struct{}{}
	q.queue = append(q.queue, id)
	q.cond.Signal()
}

// Get blocks until an item is available, returning it + shutdown=false.
// On shutdown it returns (uuid.Nil, true). The returned item is marked
// in-flight; the caller MUST call Done(id) when the pass completes so a
// concurrent Enqueue for the same key can re-queue.
func (q *workQueue) Get() (uuid.UUID, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.queue) == 0 && !q.shutdown {
		q.cond.Wait()
	}
	if q.shutdown {
		return uuid.Nil, true
	}
	id := q.queue[0]
	q.queue = q.queue[1:]
	delete(q.queued, id)
	q.inflight[id] = struct{}{}
	return id, false
}

// Done marks id's pass complete. If id was re-enqueued (marked dirty)
// while in flight, it is re-added to the queue so the new hint is
// honored.
func (q *workQueue) Done(id uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.inflight, id)
	if _, ok := q.dirty[id]; ok {
		delete(q.dirty, id)
		if _, queued := q.queued[id]; !queued {
			q.queued[id] = struct{}{}
			q.queue = append(q.queue, id)
			q.cond.Signal()
		}
	}
}

// Len returns the number of pending (not-in-flight) items. Test-only
// observability.
func (q *workQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

// Shutdown wakes every blocked Get so workers can drain and exit.
func (q *workQueue) Shutdown() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.shutdown = true
	q.cond.Broadcast()
}

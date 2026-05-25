package worktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// MergeLockKey is the canonical key inside the `locks` NATS KV bucket
// the worktree merge handler holds while a merge is in flight. See
// specs/components/nats.md §"Lock conventions".
const MergeLockKey = "merge"

// MergeLockBucket is the canonical bucket name. Pinned here so a
// future bucket rename is one constant away.
const MergeLockBucket = "locks"

// DefaultMergeLockTTL bounds how long a merge holds the lock before
// the value is treated as stale. Spec calls for 5 minutes — enough
// for a cold-cache `git merge --no-ff` on a fresh worktree, short
// enough that a crashed daemon doesn't park the lock forever.
const DefaultMergeLockTTL = 5 * time.Minute

// LockValue is the JSON shape stored under `locks.merge`. Holder
// identifies the daemon/agent grabbing the lock; AcquiredAt lets
// observers ("who's merging right now?") spot stale entries; TTL
// echoes the requested expiry so a reader doesn't have to know
// the bucket's default.
type LockValue struct {
	Holder     string    `json:"holder"`
	AcquiredAt time.Time `json:"acquired_at"`
	TTLSeconds int       `json:"ttl_seconds"`
}

// LockKV is the narrow surface AcquireMergeLock + ReleaseMergeLock
// need on the `locks` bucket. Mirrored as an interface so tests can
// substitute a fake without a live JetStream KV.
type LockKV interface {
	Create(ctx context.Context, key string, value []byte) (uint64, error)
	Get(ctx context.Context, key string) (jetstream.KeyValueEntry, error)
	Delete(ctx context.Context, key string, opts ...jetstream.KVDeleteOpt) error
}

// ErrLockHeld signals a competing holder owns the merge lock and its
// TTL has not yet expired. Callers may wait+retry or surface the
// error directly (CLI does the latter).
var ErrLockHeld = errors.New("worktree: merge lock held by another caller")

// AcquireMergeLock grabs the merge lock or returns ErrLockHeld if a
// non-stale value already sits in the bucket. The returned release
// function deletes the key; callers must invoke it exactly once
// (typical pattern: `defer release()` immediately after a successful
// acquire). The release function is safe to call after a context
// cancel — it uses its own context.
//
// Holder is recorded as the lock value (typically `<daemon-id>@<host>`
// or `<agent-uuid>`). Now is injected for deterministic tests; pass
// time.Now in production.
func AcquireMergeLock(
	ctx context.Context,
	kv LockKV,
	holder string,
	ttl time.Duration,
	now func() time.Time,
) (release func() error, err error) {
	if kv == nil {
		return nil, fmt.Errorf("worktree: lock kv is nil")
	}
	if holder == "" {
		return nil, fmt.Errorf("worktree: holder is required")
	}
	if ttl <= 0 {
		ttl = DefaultMergeLockTTL
	}
	if now == nil {
		now = time.Now
	}

	val := LockValue{
		Holder:     holder,
		AcquiredAt: now().UTC(),
		TTLSeconds: int(ttl / time.Second),
	}
	raw, err := json.Marshal(val)
	if err != nil {
		return nil, fmt.Errorf("worktree: marshal lock value: %w", err)
	}

	// Try Create. If it succeeds, the lock is ours. If it fails because
	// the key already exists, inspect the existing value — when its TTL
	// has elapsed, force-delete + retry once. After that, give up.
	if _, err := kv.Create(ctx, MergeLockKey, raw); err == nil {
		//nolint:contextcheck // release closure uses its own background ctx so a canceled caller doesn't strand the lock
		return mkRelease(kv), nil
	} else if !isAlreadyExists(err) {
		return nil, fmt.Errorf("worktree: create lock: %w", err)
	}

	// Existing value — check expiry.
	entry, err := kv.Get(ctx, MergeLockKey)
	if err != nil {
		return nil, fmt.Errorf("worktree: read existing lock: %w", err)
	}
	var existing LockValue
	if jsonErr := json.Unmarshal(entry.Value(), &existing); jsonErr != nil {
		// Garbage in the bucket — treat as stale, force-replace.
		existing = LockValue{}
	}
	expiry := existing.AcquiredAt.Add(time.Duration(existing.TTLSeconds) * time.Second)
	if !existing.AcquiredAt.IsZero() && now().UTC().Before(expiry) {
		return nil, fmt.Errorf("%w (holder=%s, acquired_at=%s, ttl=%ds)",
			ErrLockHeld, existing.Holder, existing.AcquiredAt.Format(time.RFC3339), existing.TTLSeconds)
	}
	// Stale or garbage — delete and retry the Create once.
	if delErr := kv.Delete(ctx, MergeLockKey); delErr != nil {
		return nil, fmt.Errorf("worktree: delete stale lock: %w", delErr)
	}
	if _, retryErr := kv.Create(ctx, MergeLockKey, raw); retryErr != nil {
		if isAlreadyExists(retryErr) {
			// Lost the race to another caller; that's an ErrLockHeld.
			return nil, fmt.Errorf("%w (race after stale-lock cleanup)", ErrLockHeld)
		}
		return nil, fmt.Errorf("worktree: re-create lock after stale cleanup: %w", retryErr)
	}
	//nolint:contextcheck // release closure uses its own background ctx so a canceled caller doesn't strand the lock
	return mkRelease(kv), nil
}

// acquireMergeLockWithWait wraps AcquireMergeLock with a bounded
// polling loop: on ErrLockHeld, sleep mergeLockRetryInterval and
// retry until ctx is canceled or the cumulative wait exceeds the
// bound. The bound is the lock's own TTL — a holder that's past
// its TTL is reclaimed by AcquireMergeLock itself, so any caller
// waiting beyond one TTL would be wedged behind a healthy peer
// (and the right reply to that caller is "lock held" so they can
// report back rather than hang indefinitely). Used by Manager.
// Merge; exposed unexported because the wait policy belongs to
// Merge, not to the primitive.
func acquireMergeLockWithWait(
	ctx context.Context,
	kv LockKV,
	holder string,
	ttl time.Duration,
	now func() time.Time,
) (release func() error, err error) {
	if ttl <= 0 {
		ttl = DefaultMergeLockTTL
	}
	deadline := time.Now().Add(ttl)
	for {
		rel, acquireErr := AcquireMergeLock(ctx, kv, holder, ttl, now)
		if acquireErr == nil {
			return rel, nil
		}
		if !errors.Is(acquireErr, ErrLockHeld) {
			return nil, acquireErr
		}
		if time.Now().After(deadline) {
			return nil, acquireErr
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("worktree: wait for merge lock: %w", ctx.Err())
		case <-time.After(mergeLockRetryInterval):
		}
	}
}

// mergeLockRetryInterval is the poll cadence
// acquireMergeLockWithWait uses while parked on ErrLockHeld. Short
// enough that a serialized peer cuts the lock release latency to
// the noise floor; long enough that a wedged lock doesn't burn
// CPU on Get calls.
const mergeLockRetryInterval = 50 * time.Millisecond

// mkRelease returns the release closure used by AcquireMergeLock.
// Pulled out so the success and stale-retry paths share one
// implementation.
func mkRelease(kv LockKV) func() error {
	return func() error {
		// Use a fresh context for release so a canceled caller ctx
		// doesn't strand the lock. 5s is enough for a healthy NATS.
		relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := kv.Delete(relCtx, MergeLockKey); err != nil {
			if errors.Is(err, jetstream.ErrKeyNotFound) {
				return nil
			}
			return fmt.Errorf("worktree: delete merge lock: %w", err)
		}
		return nil
	}
}

// isAlreadyExists matches jetstream's "key exists, can't Create"
// signal. The driver exposes this as ErrKeyExists since 1.x; older
// versions surface it as a generic error with the right message.
func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, jetstream.ErrKeyExists) {
		return true
	}
	// Fall-through: match the text the server returns. Cheap defensive
	// check so a small driver version drift doesn't lose us a lock.
	msg := err.Error()
	return strings.Contains(msg, "wrong last sequence") || strings.Contains(msg, "key exists")
}

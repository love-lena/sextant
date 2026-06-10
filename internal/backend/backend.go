// Package backend defines the stream substrate the bus implements the protocol
// against (ADR-0018, ADR-0019): one internal interface, satisfied by a module
// per backend — NATS today (internal/backend/natsbackend), Redis later. Each
// method is shaped to the semantic contract (protocol/semantic-contract.md) and
// checked against "how would Redis satisfy this?", so the seam stays
// backend-portable rather than NATS-shaped.
//
// The backend is a deep module behind a narrow interface: a pure bytes-and-
// revisions store plus a durable, ordered, replayable log. Frame semantics — the
// id, author, kind, epoch, and the artifact timestamps — live in the bus, which
// stamps the frame and stores its bytes here; the backend never parses a frame.
package backend

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors a backend maps its native failures to, so the bus can turn a
// failure into a protocol result without importing a backend's error types.
var (
	// ErrKeyExists is returned by Create when the key is already present.
	ErrKeyExists = errors.New("backend: key exists")
	// ErrRevisionMismatch is returned by CompareAndSet when the current revision
	// is not the expected one (a concurrent write moved it on).
	ErrRevisionMismatch = errors.New("backend: revision mismatch")
	// ErrNotFound is returned by Get and CompareAndSet when the key is absent.
	ErrNotFound = errors.New("backend: not found")
	// ErrSequenceGone is returned by Subscribe(StartFromSeq) when the requested
	// resume sequence is beyond the stream head — the store was wiped or the
	// history expired. The SDK maps this to a loud subscriber error (ADR-0027).
	ErrSequenceGone = errors.New("backend: sequence gone")
)

// LogEntry is one record in the durable log, with the substrate-assigned
// position and timestamp the bus trusts (ADR-0006).
type LogEntry struct {
	Subject string
	Seq     uint64 // monotonic position; also the read cursor
	Data    []byte
	Time    time.Time // substrate-stamped; the trusted clock
}

// Start says where a Subscribe begins.
type Start int

const (
	// StartNew delivers only entries appended after the subscription starts.
	StartNew Start = iota
	// StartAll replays retained history first, then live entries.
	StartAll
	// StartFromSeq resumes from a specific stream sequence (inclusive). The
	// sequence is passed as sinceSeq to Subscribe; it is the first sequence to
	// deliver, so resuming from last-delivered+1 closes the gap without
	// duplicating the last message. If the sequence is beyond the head of the
	// retained log (the store was wiped) the backend returns an error.
	StartFromSeq
)

// Change is one update delivered by Watch: the value at this revision, or a
// deletion (Deleted true, Value nil).
type Change struct {
	Value    []byte
	Revision uint64
	Deleted  bool
}

// Backend is the stream substrate the bus runs on. Implementations must be safe
// for concurrent use by multiple goroutines.
type Backend interface {
	// --- Durable, ordered, replayable log (the Messages substrate) ---

	// Append appends data to the log on subject and returns its assigned
	// monotonic sequence. The entry is durable: it survives until retention
	// expires, even with no subscriber.
	Append(ctx context.Context, subject string, data []byte) (seq uint64, err error)

	// Read returns up to limit entries matching subject (exact or wildcard) at or
	// after cursor since (0 = start of retained history), plus the cursor to
	// resume from. Passing next unchanged to the following Read yields no gaps and
	// no duplicates. The cursor is a bus-opaque monotonic token the backend
	// synthesizes; the bus never interprets its value.
	Read(ctx context.Context, subject string, since uint64, limit int) (entries []LogEntry, next uint64, err error)

	// Subscribe streams entries matching subject from start on the returned
	// channel until ctx is cancelled (which closes the channel). The bus owns the
	// position; the backend keeps no per-subscriber replay state.
	// sinceSeq is only used when start == StartFromSeq; it is the first stream
	// sequence to deliver (inclusive). Pass 0 for StartNew and StartAll.
	Subscribe(ctx context.Context, subject string, start Start, sinceSeq uint64) (<-chan LogEntry, error)

	// --- Named, versioned records (the Artifacts + registry substrate) ---

	// Create stores value under key in bucket, failing with ErrKeyExists if the
	// key is already present. The first revision is 1.
	Create(ctx context.Context, bucket, key string, value []byte) (rev uint64, err error)

	// Put stores value under key unconditionally (creating or overwriting) and
	// returns the new revision. Used for last-writer-wins records — the clients
	// registry and protocol metadata — not for compare-and-set artifacts.
	Put(ctx context.Context, bucket, key string, value []byte) (rev uint64, err error)

	// CompareAndSet updates key only if its current revision equals expected,
	// returning the new revision; ErrRevisionMismatch otherwise, or ErrNotFound if
	// the key is absent.
	CompareAndSet(ctx context.Context, bucket, key string, value []byte, expected uint64) (rev uint64, err error)

	// Get returns the current value and revision for key, or ErrNotFound.
	Get(ctx context.Context, bucket, key string) (value []byte, rev uint64, err error)

	// Delete removes key. It is unconditional (the reference surface); deleting an
	// absent key is not an error.
	Delete(ctx context.Context, bucket, key string) error

	// Watch streams changes to key on the returned channel: the current value
	// first (if present), then each later write and delete, until ctx is cancelled
	// (which closes the channel).
	Watch(ctx context.Context, bucket, key string) (<-chan Change, error)

	// Keys enumerates the keys present in bucket. An empty bucket yields an empty
	// slice, not an error.
	Keys(ctx context.Context, bucket string) ([]string, error)
}

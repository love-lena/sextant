// Package natsbackend implements backend.Backend over NATS JetStream — the first
// backend module (ADR-0018, ADR-0019). It is the only place JetStream/KV
// specifics live; the bus implements the operations against backend.Backend and
// never sees NATS. The implementation notes are in protocol/nats-binding.md.
//
// The log (Append/Read/Subscribe) maps to a JetStream stream + ephemeral
// consumers (no per-subscriber server state — the bus owns the cursor). The
// versioned records (Create/Put/CompareAndSet/Get/Delete/Watch/Keys) map to a KV
// bucket, whose revision is the cursor for compare-and-set.
package natsbackend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/love-lena/sextant/internal/backend"
	"github.com/nats-io/nats.go/jetstream"
)

// Backend implements backend.Backend over a JetStream context.
type Backend struct {
	js        jetstream.JetStream
	logStream string // the stream capturing the log subjects (e.g. MESSAGES)
}

// New builds a NATS backend over js. logStream is the name of the JetStream
// stream that captures the log subjects (Append publishes by subject; Read and
// Subscribe filter this stream by subject).
func New(js jetstream.JetStream, logStream string) *Backend {
	return &Backend{js: js, logStream: logStream}
}

var _ backend.Backend = (*Backend)(nil)

// --- log ---

// Append publishes data to subject and returns the assigned stream sequence.
func (b *Backend) Append(ctx context.Context, subject string, data []byte) (uint64, error) {
	ack, err := b.js.Publish(ctx, subject, data)
	if err != nil {
		return 0, fmt.Errorf("natsbackend: append %s: %w", subject, err)
	}
	return ack.Sequence, nil
}

// Read fetches up to limit entries matching subject at or after the cursor since
// (0 = from the start of retained history), via a short-lived ephemeral consumer.
func (b *Backend) Read(ctx context.Context, subject string, since uint64, limit int) ([]backend.LogEntry, uint64, error) {
	stream, err := b.js.Stream(ctx, b.logStream)
	if err != nil {
		return nil, since, fmt.Errorf("natsbackend: open stream %s: %w", b.logStream, err)
	}
	cfg := jetstream.OrderedConsumerConfig{FilterSubjects: []string{subject}}
	if since == 0 {
		cfg.DeliverPolicy = jetstream.DeliverAllPolicy
	} else {
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = since
	}
	cons, err := stream.OrderedConsumer(ctx, cfg)
	if err != nil {
		return nil, since, fmt.Errorf("natsbackend: read consumer: %w", err)
	}
	batch, err := cons.Fetch(limit, jetstream.FetchMaxWait(500*time.Millisecond))
	if err != nil {
		return nil, since, fmt.Errorf("natsbackend: fetch: %w", err)
	}
	var out []backend.LogEntry
	next := since
	for msg := range batch.Messages() {
		md, err := msg.Metadata()
		if err != nil {
			continue
		}
		out = append(out, backend.LogEntry{
			Subject: msg.Subject(),
			Seq:     md.Sequence.Stream,
			Data:    msg.Data(),
			Time:    md.Timestamp,
		})
		next = md.Sequence.Stream + 1
	}
	if err := batch.Error(); err != nil {
		return out, next, fmt.Errorf("natsbackend: read: %w", err)
	}
	return out, next, nil
}

// Subscribe streams entries matching subject from start until ctx is cancelled.
// sinceSeq is used only when start == backend.StartFromSeq; it is the first
// stream sequence to deliver (inclusive).
func (b *Backend) Subscribe(ctx context.Context, subject string, start backend.Start, sinceSeq uint64) (<-chan backend.LogEntry, error) {
	stream, err := b.js.Stream(ctx, b.logStream)
	if err != nil {
		return nil, fmt.Errorf("natsbackend: open stream %s: %w", b.logStream, err)
	}
	cfg := jetstream.OrderedConsumerConfig{FilterSubjects: []string{subject}}
	switch start {
	case backend.StartAll:
		cfg.DeliverPolicy = jetstream.DeliverAllPolicy
	case backend.StartFromSeq:
		// Guard against a wiped or heavily-expired stream: if the requested resume
		// sequence is beyond last+1, the messages we previously delivered no longer
		// exist. Return an error so the SDK can surface it loudly (ADR-0027) rather
		// than wait silently for a sequence that may never arrive.
		info, err := stream.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("natsbackend: stream info for resume check: %w", err)
		}
		if sinceSeq > info.State.LastSeq+1 {
			return nil, fmt.Errorf("natsbackend: %w: resume sequence %d is beyond stream head %d (store may have been wiped or history expired)",
				backend.ErrSequenceGone, sinceSeq, info.State.LastSeq)
		}
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = sinceSeq
	default: // StartNew
		cfg.DeliverPolicy = jetstream.DeliverNewPolicy
	}
	cons, err := stream.OrderedConsumer(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("natsbackend: subscribe consumer: %w", err)
	}
	iter, err := cons.Messages()
	if err != nil {
		return nil, fmt.Errorf("natsbackend: subscribe: %w", err)
	}
	ch := make(chan backend.LogEntry)
	// One goroutine owns ch: it forwards until ctx is cancelled (a second
	// goroutine stops the iterator, which unblocks Next), then closes ch.
	go func() {
		<-ctx.Done()
		iter.Stop()
	}()
	go func() {
		defer close(ch)
		for {
			msg, err := iter.Next()
			if err != nil {
				return // iterator stopped (ctx cancelled) or fatal
			}
			md, err := msg.Metadata()
			if err != nil {
				continue
			}
			select {
			case ch <- backend.LogEntry{
				Subject: msg.Subject(),
				Seq:     md.Sequence.Stream,
				Data:    msg.Data(),
				Time:    md.Timestamp,
			}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// --- versioned records ---

func (b *Backend) kv(ctx context.Context, bucket string) (jetstream.KeyValue, error) {
	kv, err := b.js.KeyValue(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("natsbackend: open bucket %s: %w", bucket, err)
	}
	return kv, nil
}

// Create stores value under key, failing with backend.ErrKeyExists if present.
func (b *Backend) Create(ctx context.Context, bucket, key string, value []byte) (uint64, error) {
	kv, err := b.kv(ctx, bucket)
	if err != nil {
		return 0, err
	}
	rev, err := kv.Create(ctx, key, value)
	if errors.Is(err, jetstream.ErrKeyExists) {
		return 0, backend.ErrKeyExists
	}
	if err != nil {
		return 0, fmt.Errorf("natsbackend: create %s/%s: %w", bucket, key, err)
	}
	return rev, nil
}

// Put stores value under key unconditionally and returns the new revision.
func (b *Backend) Put(ctx context.Context, bucket, key string, value []byte) (uint64, error) {
	kv, err := b.kv(ctx, bucket)
	if err != nil {
		return 0, err
	}
	rev, err := kv.Put(ctx, key, value)
	if err != nil {
		return 0, fmt.Errorf("natsbackend: put %s/%s: %w", bucket, key, err)
	}
	return rev, nil
}

// CompareAndSet updates key only if its current revision equals expected.
func (b *Backend) CompareAndSet(ctx context.Context, bucket, key string, value []byte, expected uint64) (uint64, error) {
	kv, err := b.kv(ctx, bucket)
	if err != nil {
		return 0, err
	}
	rev, err := kv.Update(ctx, key, value, expected)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return 0, backend.ErrNotFound
	}
	if isWrongLastSeq(err) {
		return 0, backend.ErrRevisionMismatch
	}
	if err != nil {
		return 0, fmt.Errorf("natsbackend: cas %s/%s (rev %d): %w", bucket, key, expected, err)
	}
	return rev, nil
}

// Get returns the current value and revision for key, or backend.ErrNotFound.
func (b *Backend) Get(ctx context.Context, bucket, key string) ([]byte, uint64, error) {
	kv, err := b.kv(ctx, bucket)
	if err != nil {
		return nil, 0, err
	}
	e, err := kv.Get(ctx, key)
	if errors.Is(err, jetstream.ErrKeyNotFound) {
		return nil, 0, backend.ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("natsbackend: get %s/%s: %w", bucket, key, err)
	}
	return e.Value(), e.Revision(), nil
}

// Delete removes key (unconditional; absent key is not an error).
func (b *Backend) Delete(ctx context.Context, bucket, key string) error {
	kv, err := b.kv(ctx, bucket)
	if err != nil {
		return err
	}
	if err := kv.Delete(ctx, key); err != nil {
		return fmt.Errorf("natsbackend: delete %s/%s: %w", bucket, key, err)
	}
	return nil
}

// Watch streams changes to key: current value first, then writes and deletes.
func (b *Backend) Watch(ctx context.Context, bucket, key string) (<-chan backend.Change, error) {
	kv, err := b.kv(ctx, bucket)
	if err != nil {
		return nil, err
	}
	w, err := kv.Watch(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("natsbackend: watch %s/%s: %w", bucket, key, err)
	}
	ch := make(chan backend.Change)
	go func() {
		defer close(ch)
		defer func() { _ = w.Stop() }()
		for {
			select {
			case <-ctx.Done():
				return
			case e, ok := <-w.Updates():
				if !ok {
					return
				}
				if e == nil {
					continue // marks the end of the initial replay
				}
				select {
				case ch <- backend.Change{
					Value:    e.Value(),
					Revision: e.Revision(),
					Deleted:  e.Operation() != jetstream.KeyValuePut,
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return ch, nil
}

// Keys enumerates the keys present in bucket (empty bucket → empty slice).
func (b *Backend) Keys(ctx context.Context, bucket string) ([]string, error) {
	kv, err := b.kv(ctx, bucket)
	if err != nil {
		return nil, err
	}
	keys, err := kv.Keys(ctx)
	if errors.Is(err, jetstream.ErrNoKeysFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("natsbackend: keys %s: %w", bucket, err)
	}
	return keys, nil
}

// isWrongLastSeq reports whether err is JetStream's compare-and-set failure (the
// current revision was not the expected one).
func isWrongLastSeq(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *jetstream.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode == jetstream.JSErrCodeStreamWrongLastSequence
	}
	return false
}

package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// KVOp identifies the kind of change reported by WatchKV.
type KVOp string

const (
	KVOpPut    KVOp = "put"
	KVOpDelete KVOp = "delete"
	KVOpPurge  KVOp = "purge"
)

// KVUpdate is one observed change on a KV key.
type KVUpdate struct {
	Bucket    string
	Key       string
	Value     []byte
	Revision  uint64
	Op        KVOp
	Timestamp time.Time
}

// PutKV stores value at key in bucket. Creates the entry if missing or
// overwrites the existing value. The KV bucket must already exist —
// natsboot.Bootstrap is the sole bucket creator; PutKV does NOT create
// buckets on demand because that would hide schema errors.
func (c *Client) PutKV(ctx context.Context, bucket, key string, value []byte) error {
	if c.isClosed() {
		return ErrClosed
	}
	if bucket == "" || key == "" {
		return fmt.Errorf("client: PutKV requires bucket and key")
	}
	kv, err := c.js.KeyValue(ctx, bucket)
	if err != nil {
		return fmt.Errorf("client: open kv bucket %q: %w", bucket, err)
	}
	if _, err := kv.Put(ctx, key, value); err != nil {
		return fmt.Errorf("client: put kv %q/%q: %w", bucket, key, err)
	}
	return nil
}

// GetKV reads the current value of key from bucket. Returns ErrKVKeyNotFound
// when the key is absent.
func (c *Client) GetKV(ctx context.Context, bucket, key string) ([]byte, error) {
	if c.isClosed() {
		return nil, ErrClosed
	}
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("client: GetKV requires bucket and key")
	}
	kv, err := c.js.KeyValue(ctx, bucket)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketNotFound) {
			return nil, fmt.Errorf("client: kv bucket %q: %w", bucket, ErrKVKeyNotFound)
		}
		return nil, fmt.Errorf("client: open kv bucket %q: %w", bucket, err)
	}
	entry, err := kv.Get(ctx, key)
	if err != nil {
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil, ErrKVKeyNotFound
		}
		return nil, fmt.Errorf("client: get kv %q/%q: %w", bucket, key, err)
	}
	return entry.Value(), nil
}

// WatchKV subscribes to changes on the named key in bucket and returns a
// channel of updates. The channel closes when ctx is canceled OR when
// Client.Close is called — registering the watcher with the Client
// guarantees no goroutine leaks even if the caller passed a
// long-lived ctx like context.Background().
//
// On subscription, the current value of the key (if any) is delivered
// first, followed by live updates. Initial-state delivery matches
// NATS's KeyWatcher contract: a nil sentinel from the underlying watcher
// marks the boundary and is dropped by this wrapper.
func (c *Client) WatchKV(ctx context.Context, bucket, key string) (<-chan KVUpdate, error) {
	if c.isClosed() {
		return nil, ErrClosed
	}
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("client: WatchKV requires bucket and key")
	}
	kv, err := c.js.KeyValue(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("client: open kv bucket %q: %w", bucket, err)
	}
	watcher, err := kv.Watch(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("client: watch kv %q/%q: %w", bucket, key, err)
	}

	// Tie ctx cancellation and Client.Close into the same cancel
	// signal so the loop exits in both cases.
	watchCtx, cancelWatch := context.WithCancel(ctx)
	reg := c.register(func() { cancelWatch() })

	out := make(chan KVUpdate, 16)
	go func() {
		defer close(out)
		defer func() { _ = watcher.Stop() }()
		defer c.deregister(reg)
		updates := watcher.Updates()
		for {
			select {
			case <-watchCtx.Done():
				return
			case entry, ok := <-updates:
				if !ok {
					return
				}
				if entry == nil {
					// Initial-state boundary marker — drop.
					continue
				}
				select {
				case out <- KVUpdate{
					Bucket:    entry.Bucket(),
					Key:       entry.Key(),
					Value:     entry.Value(),
					Revision:  entry.Revision(),
					Op:        translateKVOp(entry.Operation()),
					Timestamp: entry.Created(),
				}:
				case <-watchCtx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func translateKVOp(op jetstream.KeyValueOp) KVOp {
	switch op {
	case jetstream.KeyValuePut:
		return KVOpPut
	case jetstream.KeyValueDelete:
		return KVOpDelete
	case jetstream.KeyValuePurge:
		return KVOpPurge
	default:
		return KVOpPut
	}
}

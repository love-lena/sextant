package natsboot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Bootstrap creates every stream and KV bucket described by Streams() and
// KVBuckets() against the JetStream context built from nc. The operation
// is idempotent: existing streams/buckets are reconciled with the
// spec via CreateOrUpdateStream / CreateOrUpdateKeyValue.
//
// Bootstrap does not own nc; the caller closes it.
func Bootstrap(ctx context.Context, nc *nats.Conn, maxBytes int64) error {
	if nc == nil {
		return fmt.Errorf("natsboot: nil nats.Conn")
	}
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("natsboot: jetstream.New: %w", err)
	}
	if err := createStreams(ctx, js, Streams(maxBytes)); err != nil {
		return err
	}
	if err := createKVBuckets(ctx, js, KVBuckets()); err != nil {
		return err
	}
	return nil
}

func createStreams(ctx context.Context, js jetstream.JetStream, specs []StreamSpec) error {
	for _, s := range specs {
		cfg := jetstream.StreamConfig{
			Name:      s.Name,
			Subjects:  s.Subjects,
			Storage:   jetstream.FileStorage,
			Retention: translateRetention(s.Retention),
			MaxAge:    s.MaxAge,
			MaxBytes:  s.MaxBytes,
			Replicas:  1,
		}
		if _, err := js.CreateOrUpdateStream(ctx, cfg); err != nil {
			return fmt.Errorf("natsboot: create/update stream %q: %w", s.Name, err)
		}
	}
	return nil
}

func createKVBuckets(ctx context.Context, js jetstream.JetStream, specs []KVSpec) error {
	for _, b := range specs {
		cfg := jetstream.KeyValueConfig{
			Bucket:      b.Bucket,
			Description: b.Description,
			History:     b.History,
			Storage:     jetstream.FileStorage,
			Replicas:    1,
		}
		if b.TTL > 0 {
			cfg.TTL = b.TTL
		}
		if _, err := js.CreateOrUpdateKeyValue(ctx, cfg); err != nil {
			return fmt.Errorf("natsboot: create/update kv bucket %q: %w", b.Bucket, err)
		}
	}
	return nil
}

func translateRetention(p RetentionPolicy) jetstream.RetentionPolicy {
	switch p {
	case RetentionInterest:
		return jetstream.InterestPolicy
	case RetentionWorkQ:
		return jetstream.WorkQueuePolicy
	case RetentionLimits, "":
		return jetstream.LimitsPolicy
	default:
		return jetstream.LimitsPolicy
	}
}

// VerifyBootstrap re-fetches every stream and KV bucket and asserts that
// it exists. Used by tests and `sextant-natsboot --verify`.
func VerifyBootstrap(ctx context.Context, nc *nats.Conn) error {
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("natsboot: jetstream.New: %w", err)
	}
	for _, s := range Streams(0) {
		if _, err := js.Stream(ctx, s.Name); err != nil {
			return fmt.Errorf("natsboot: missing stream %q: %w", s.Name, err)
		}
	}
	for _, b := range KVBuckets() {
		if _, err := js.KeyValue(ctx, b.Bucket); err != nil {
			if errors.Is(err, jetstream.ErrBucketNotFound) {
				return fmt.Errorf("natsboot: missing kv bucket %q", b.Bucket)
			}
			return fmt.Errorf("natsboot: lookup kv bucket %q: %w", b.Bucket, err)
		}
	}
	return nil
}

// WaitForStream polls JetStream until the named stream is fetchable or
// the context expires. Useful in tests that race against bootstrap.
func WaitForStream(ctx context.Context, nc *nats.Conn, name string) error {
	js, err := jetstream.New(nc)
	if err != nil {
		return fmt.Errorf("natsboot: jetstream.New: %w", err)
	}
	for {
		if _, err := js.Stream(ctx, name); err == nil {
			return nil
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

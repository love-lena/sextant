package bus

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
)

// SetFreshnessWindow overrides the heartbeat freshness window so a test can make
// a seeded last_seen count as fresh or stale deterministically (TASK-126).
func (b *Bus) SetFreshnessWindow(d time.Duration) { b.freshnessWindow = d }

// Test-only seams. This file is a _test.go file, so it is compiled only into the
// bus package's test binary and never ships in any production build — yet because
// it is package bus it can reach the unexported backend. It re-exports the
// privileged writes the bus performs as the operator so the external bus_test
// integration suite can set up state a client cannot create for itself: a
// different protocol epoch, a hand-seeded or corrupt registry record, a raw frame
// that bypasses the bus's stamping. That suite drives the real SDK against these,
// exercising the fail-loud and quarantine paths the per-client allow-list now
// makes unreachable from a client. See docs/conventions/test-features.md (rung 3): the
// test lives in the package that owns the internals it pokes, so no build tag and
// no production surface are needed.

// SetEpoch overwrites the bus's published protocol epoch, so a subsequent connect
// under a different compiled epoch fails the hard-gate.
func (b *Bus) SetEpoch(ctx context.Context, epoch int) error {
	if _, err := b.backend.Put(ctx, sx.BucketMeta, sx.MetaKeyEpoch, []byte(strconv.Itoa(epoch))); err != nil {
		return fmt.Errorf("bus: set epoch: %w", err)
	}
	return nil
}

// SeedClientRecord writes a raw registry record under id, bypassing the register
// handshake. The bytes are stored verbatim (they may be malformed), so the
// listing path's robustness can be exercised.
func (b *Bus) SeedClientRecord(ctx context.Context, id string, record []byte) error {
	if _, err := b.backend.Put(ctx, sx.BucketClients, id, record); err != nil {
		return fmt.Errorf("bus: seed client record %q: %w", id, err)
	}
	return nil
}

// DeleteClientRecord removes a client's registry record out from under it,
// regardless of whether its connection is still live — the way to force the
// empty-directory and stale-entry paths.
func (b *Bus) DeleteClientRecord(ctx context.Context, id string) error {
	if err := b.backend.Delete(ctx, sx.BucketClients, id); err != nil {
		return fmt.Errorf("bus: delete client record %q: %w", id, err)
	}
	return nil
}

// InjectMessage appends raw bytes to the messages log on subject, bypassing the
// bus's frame stamping — the only way to place a frame a client could never
// publish (stale clock, wrong epoch, malformed) so the SDK's consume-side
// quarantine can be tested. Returns the assigned sequence.
func (b *Bus) InjectMessage(ctx context.Context, subject string, frame []byte) (uint64, error) {
	seq, err := b.backend.Append(ctx, subject, frame)
	if err != nil {
		return 0, fmt.Errorf("bus: inject message on %q: %w", subject, err)
	}
	return seq, nil
}

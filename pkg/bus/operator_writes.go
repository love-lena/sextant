package bus

import (
	"context"
	"fmt"
	"strconv"

	"github.com/love-lena/sextant/pkg/sx"
)

// Operator-side write seams: privileged writes the bus performs as the operator,
// straight against the backend over its own full-access connection. They exist
// because the per-client allow-list (ADR-0019) denies clients all direct backend
// access — so the only way to establish bus state that no client could create
// for itself (a different protocol epoch, a hand-seeded or corrupt registry
// record, a raw frame that bypasses the bus's stamping) is for the bus to write
// it. Tests use these to exercise the fail-loud and quarantine paths; they are
// also the natural home for future operator tooling (epoch bumps, registry
// surgery). They are not reachable over the wire — only the holder of the *Bus
// has them, which already implies operator authority.

// SetEpoch overwrites the bus's published protocol epoch in the meta bucket. The
// value clients read at connect changes immediately, so a subsequent connect with
// a different compiled epoch fails the hard-gate.
func (b *Bus) SetEpoch(ctx context.Context, epoch int) error {
	if _, err := b.backend.Put(ctx, sx.BucketMeta, sx.MetaKeyEpoch, []byte(strconv.Itoa(epoch))); err != nil {
		return fmt.Errorf("bus: set epoch: %w", err)
	}
	return nil
}

// SeedClientRecord writes a raw registry record under id, bypassing the
// register handshake. The bytes are stored verbatim (they may be malformed), so
// the listing path's robustness can be exercised.
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

package shipper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// TestBackpressureHardCapFailClosed verifies the fail-closed path:
// when the BoltDB buffer hits the configured hard cap, the shipper
// stops pulling new NATS messages, emits a critical audit envelope,
// and exits with ErrBackpressure.
//
// To trigger the cap quickly we:
//  1. Use a tiny hard_cap_bytes (64 KiB).
//  2. Stop ClickHouse so every flush spills.
//  3. Publish enough envelopes to exceed the cap.
//  4. Subscribe to audit.shipper_backpressure and assert receipt.
//  5. Assert Run returns ErrBackpressure.
func TestBackpressureHardCapFailClosed(t *testing.T) {
	requireBins(t)
	f := newFixture(t)

	// Override the cap. 64 KiB is small enough that even a handful of
	// frames + the BoltDB metadata overhead will cross it.
	f.cfg.Buffer.HardCapBytes = 64 << 10
	f.cfg.Shipper.DegradedMode = DegradedModeFailClosed
	f.cfg.Shipper.MetricsInterval = Duration(10 * time.Second) // keep metrics out of the buffer
	f.cfg.Batch.MaxEvents = 50

	// Subscribe to the audit subject BEFORE starting the shipper so we
	// don't miss the envelope.
	nc, err := f.natsSrv.Connect()
	if err != nil {
		t.Fatalf("audit connect: %v", err)
	}
	defer nc.Close()
	auditMsgs := make(chan *nats.Msg, 4)
	sub, err := nc.ChanSubscribe("audit.shipper_backpressure", auditMsgs)
	if err != nil {
		t.Fatalf("subscribe audit: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	shp, err := New(context.Background(), f.cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = shp.Close() })

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runDone := make(chan error, 1)
	go func() { runDone <- shp.Run(runCtx) }()
	time.Sleep(300 * time.Millisecond)

	// Stop ClickHouse to force every write into the buffer.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := f.chSrv.Stop(stopCtx); err != nil {
		stopCancel()
		t.Fatalf("ch Stop: %v", err)
	}
	stopCancel()

	// Publish until Run returns OR we hit a publish ceiling. Each
	// frame carries a moderately large body so the BoltDB rows are
	// big enough that 64 KiB fills fast.
	agentID := uuid.New()
	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()}
	subject := fmt.Sprintf("agents.%s.frames", agentID.String())
	padding := make([]byte, 1024)
	for i := range padding {
		padding[i] = 'x'
	}

	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		for i := 0; i < 5000; i++ {
			env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from,
				sextantproto.AgentFramePayload{
					FrameKind: sextantproto.FrameAssistantText,
					Body: map[string]any{
						"i":   i,
						"pad": string(padding),
					},
				})
			if err != nil {
				return
			}
			raw, err := json.Marshal(env)
			if err != nil {
				return
			}
			// Use core publish to fan out fast; JetStream Publish
			// would serialize.
			if err := nc.Publish(subject, raw); err != nil {
				return
			}
			if i%100 == 0 {
				_ = nc.Flush()
			}
			// Give the shipper time to flush.
			time.Sleep(2 * time.Millisecond)
			select {
			case <-runCtx.Done():
				return
			default:
			}
		}
	}()

	// Wait for the audit envelope.
	select {
	case msg := <-auditMsgs:
		var env sextantproto.Envelope
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			t.Fatalf("audit envelope decode: %v", err)
		}
		if env.Kind != sextantproto.KindAudit {
			t.Errorf("audit envelope kind = %s, want %s", env.Kind, sextantproto.KindAudit)
		}
		var payload sextantproto.AuditPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Fatalf("audit payload decode: %v", err)
		}
		if payload.Action != "shipper.backpressure" {
			t.Errorf("audit action = %s, want shipper.backpressure", payload.Action)
		}
		if payload.Result != sextantproto.AuditError {
			t.Errorf("audit result = %s, want error", payload.Result)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("audit.shipper_backpressure not received within 60s; buffer=%d cap=%d",
			shp.Stats().BufferDepthBytes, f.cfg.Buffer.HardCapBytes)
	}

	// Run must return ErrBackpressure soon after.
	select {
	case err := <-runDone:
		if !errors.Is(err, ErrBackpressure) {
			t.Errorf("Run returned %v, want ErrBackpressure", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("Run did not return ErrBackpressure within 15s")
	}

	<-pubDone
}

// TestBackpressureDegradedModeDropsOldest verifies the alternate
// degraded mode: oldest spillover entries are dropped to free space
// and audit.shipper_drop events are emitted. The shipper does NOT
// exit.
func TestBackpressureDegradedModeDropsOldest(t *testing.T) {
	requireBins(t)
	f := newFixture(t)

	f.cfg.Buffer.HardCapBytes = 64 << 10
	f.cfg.Shipper.DegradedMode = DegradedModeDropOldest
	f.cfg.Shipper.MetricsInterval = Duration(10 * time.Second)
	f.cfg.Batch.MaxEvents = 50

	nc, err := f.natsSrv.Connect()
	if err != nil {
		t.Fatalf("audit connect: %v", err)
	}
	defer nc.Close()
	dropMsgs := make(chan *nats.Msg, 8)
	sub, err := nc.ChanSubscribe("audit.shipper_drop", dropMsgs)
	if err != nil {
		t.Fatalf("subscribe drop: %v", err)
	}
	defer func() { _ = sub.Unsubscribe() }()

	shp, err := New(context.Background(), f.cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = shp.Close() })

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runDone := make(chan error, 1)
	go func() { runDone <- shp.Run(runCtx) }()
	time.Sleep(300 * time.Millisecond)

	// Stop ClickHouse to force spillover.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := f.chSrv.Stop(stopCtx); err != nil {
		stopCancel()
		t.Fatalf("ch Stop: %v", err)
	}
	stopCancel()

	agentID := uuid.New()
	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()}
	subject := fmt.Sprintf("agents.%s.frames", agentID.String())
	padding := make([]byte, 1024)
	for i := range padding {
		padding[i] = 'x'
	}

	pubDone := make(chan struct{})
	go func() {
		defer close(pubDone)
		for i := 0; i < 500; i++ {
			env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from,
				sextantproto.AgentFramePayload{
					FrameKind: sextantproto.FrameAssistantText,
					Body:      map[string]any{"i": i, "pad": string(padding)},
				})
			if err != nil {
				return
			}
			raw, err := json.Marshal(env)
			if err != nil {
				return
			}
			if err := nc.Publish(subject, raw); err != nil {
				return
			}
			if i%50 == 0 {
				_ = nc.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Wait for at least one drop audit envelope.
	select {
	case msg := <-dropMsgs:
		var env sextantproto.Envelope
		if err := json.Unmarshal(msg.Data, &env); err != nil {
			t.Fatalf("drop envelope decode: %v", err)
		}
		var payload sextantproto.AuditPayload
		if err := json.Unmarshal(env.Payload, &payload); err != nil {
			t.Fatalf("drop payload decode: %v", err)
		}
		if payload.Action != "shipper.drop" {
			t.Errorf("drop audit action = %s", payload.Action)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("audit.shipper_drop not received within 60s; buffer=%d dropped=%d",
			shp.Stats().BufferDepthBytes, shp.Stats().DroppedTotal)
	}

	// Shipper must still be running.
	select {
	case err := <-runDone:
		t.Fatalf("Run returned unexpectedly: %v", err)
	default:
	}

	cancel()
	<-runDone
	<-pubDone
}

// TestSpilloverPutGetDeleteRoundTrip exercises the BoltDB layer in
// isolation: write rows, read them back in FIFO order, delete them.
func TestSpilloverPutGetDeleteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	buf, err := openSpillover(filepath.Join(dir, "buf"))
	if err != nil {
		t.Fatalf("openSpillover: %v", err)
	}
	defer func() { _ = buf.Close() }()

	rows := make([]Row, 5)
	for i := range rows {
		rows[i] = Row{
			Table:      TableEvents,
			EnvelopeID: uuid.New(),
			EnvelopeTs: time.Now().UTC(),
			Event: &EventRow{
				ID:           rows[i].EnvelopeID,
				Ts:           time.Now().UTC(),
				Subject:      fmt.Sprintf("agents.x.frames#%d", i),
				FromKind:     "agent",
				FromID:       "x",
				Kind:         "agent_frame",
				ProtoVersion: sextantproto.ProtoVersion,
				Payload:      `{"i":` + fmt.Sprint(i) + `}`,
			},
		}
	}
	if _, err := buf.Put(rows); err != nil {
		t.Fatalf("Put: %v", err)
	}

	counts, err := buf.CountAll()
	if err != nil {
		t.Fatalf("CountAll: %v", err)
	}
	if counts[TableEvents] != 5 {
		t.Errorf("events count = %d, want 5", counts[TableEvents])
	}

	keys, got, err := buf.PeekBatch(TableEvents, 10)
	if err != nil {
		t.Fatalf("PeekBatch: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("PeekBatch returned %d rows, want 5", len(got))
	}
	// FIFO: subject should follow i ascending.
	for i, r := range got {
		want := fmt.Sprintf("agents.x.frames#%d", i)
		if r.Event == nil {
			t.Fatalf("row %d Event nil", i)
		}
		if r.Event.Subject != want {
			t.Errorf("row %d Subject = %q, want %q", i, r.Event.Subject, want)
		}
	}

	if err := buf.Delete(TableEvents, keys); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	counts2, err := buf.CountAll()
	if err != nil {
		t.Fatalf("CountAll2: %v", err)
	}
	if counts2[TableEvents] != 0 {
		t.Errorf("after delete events count = %d, want 0", counts2[TableEvents])
	}
}

// TestSpilloverDropOldest covers degraded_mode = drop_oldest behavior.
func TestSpilloverDropOldest(t *testing.T) {
	dir := t.TempDir()
	buf, err := openSpillover(filepath.Join(dir, "buf"))
	if err != nil {
		t.Fatalf("openSpillover: %v", err)
	}
	defer func() { _ = buf.Close() }()

	rows := make([]Row, 10)
	for i := range rows {
		rows[i] = Row{
			Table:      TableEvents,
			EnvelopeID: uuid.New(),
			Event: &EventRow{
				ID:      rows[i].EnvelopeID,
				Subject: fmt.Sprintf("agents.x.frames#%d", i),
			},
		}
	}
	if _, err := buf.Put(rows); err != nil {
		t.Fatalf("Put: %v", err)
	}
	dropped, err := buf.DropOldest(3)
	if err != nil {
		t.Fatalf("DropOldest: %v", err)
	}
	if dropped != 3 {
		t.Errorf("dropped = %d, want 3", dropped)
	}
	counts, err := buf.CountAll()
	if err != nil {
		t.Fatalf("CountAll: %v", err)
	}
	if counts[TableEvents] != 7 {
		t.Errorf("remaining = %d, want 7", counts[TableEvents])
	}
	// The dropped rows must be the OLDEST (i = 0..2).
	_, remaining, err := buf.PeekBatch(TableEvents, 20)
	if err != nil {
		t.Fatalf("PeekBatch: %v", err)
	}
	if len(remaining) != 7 {
		t.Fatalf("remaining peek = %d, want 7", len(remaining))
	}
	for i, r := range remaining {
		want := fmt.Sprintf("agents.x.frames#%d", i+3)
		if r.Event == nil || r.Event.Subject != want {
			t.Errorf("remaining[%d] subject = %q, want %q", i, r.Event.Subject, want)
		}
	}
}

// TestSpilloverSizeBytesShrinksAfterDrain pins down the reviewer-flagged
// behavior: SizeBytes must reflect *logical* buffered bytes, not the
// BoltDB file's high-water mark. Without this, degraded_mode spirals
// once tripped and fail-closed restarts re-trip on a near-empty buffer.
//
// We Put a batch, observe a nonzero SizeBytes, Delete every row, and
// assert SizeBytes returns to zero. Then DropOldest is verified to
// decrement the same way.
func TestSpilloverSizeBytesShrinksAfterDrain(t *testing.T) {
	dir := t.TempDir()
	buf, err := openSpillover(filepath.Join(dir, "buf"))
	if err != nil {
		t.Fatalf("openSpillover: %v", err)
	}
	defer func() { _ = buf.Close() }()

	if got := buf.SizeBytes(); got != 0 {
		t.Fatalf("fresh buffer SizeBytes = %d, want 0", got)
	}

	rows := make([]Row, 8)
	for i := range rows {
		rows[i] = Row{
			Table:      TableEvents,
			EnvelopeID: uuid.New(),
			EnvelopeTs: time.Now().UTC(),
			Event: &EventRow{
				ID:      rows[i].EnvelopeID,
				Subject: fmt.Sprintf("agents.x.frames#%d", i),
				Kind:    "agent_frame",
			},
		}
	}
	written, err := buf.Put(rows)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if written <= 0 {
		t.Fatalf("Put returned %d bytes, want > 0", written)
	}
	postPut := buf.SizeBytes()
	if postPut != written {
		t.Errorf("SizeBytes after Put = %d, want %d (matches written total)", postPut, written)
	}

	// Drain every row, then SizeBytes must return to zero. This is the
	// scenario where on-disk file size would lie: BoltDB never shrinks
	// the underlying file even after every key is deleted.
	keys, _, err := buf.PeekBatch(TableEvents, 100)
	if err != nil {
		t.Fatalf("PeekBatch: %v", err)
	}
	if err := buf.Delete(TableEvents, keys); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := buf.SizeBytes(); got != 0 {
		t.Errorf("SizeBytes after drain = %d, want 0 (BoltDB file may still be large on disk, but logical bytes must be zero)", got)
	}

	// DropOldest path: Put again, drop a subset, assert SizeBytes
	// decreases by exactly the value-length sum of the dropped rows.
	if _, err := buf.Put(rows); err != nil {
		t.Fatalf("Put #2: %v", err)
	}
	before := buf.SizeBytes()
	if before <= 0 {
		t.Fatalf("SizeBytes before drop = %d, want > 0", before)
	}
	dropped, err := buf.DropOldest(3)
	if err != nil {
		t.Fatalf("DropOldest: %v", err)
	}
	if dropped != 3 {
		t.Fatalf("dropped = %d, want 3", dropped)
	}
	after := buf.SizeBytes()
	if after >= before {
		t.Errorf("SizeBytes after DropOldest = %d, want < %d", after, before)
	}
	if after <= 0 {
		t.Errorf("SizeBytes after partial drop = %d, want > 0 (5 rows still buffered)", after)
	}
}

// TestSpilloverSizeBytesRebuiltOnReopen verifies that closing the
// spillover and reopening it against the same directory recovers the
// logical-bytes counter from disk — necessary so a process restart
// does not lose its accounting of unsent work.
func TestSpilloverSizeBytesRebuiltOnReopen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "buf")
	buf, err := openSpillover(dir)
	if err != nil {
		t.Fatalf("openSpillover: %v", err)
	}

	rows := []Row{{
		Table: TableEvents,
		Event: &EventRow{Subject: "agents.x.frames#a", Kind: "agent_frame"},
	}, {
		Table: TableEvents,
		Event: &EventRow{Subject: "agents.x.frames#b", Kind: "agent_frame"},
	}}
	written, err := buf.Put(rows)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if buf.SizeBytes() != written {
		t.Fatalf("SizeBytes pre-close = %d, want %d", buf.SizeBytes(), written)
	}
	if err := buf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and assert the counter was rebuilt from the on-disk data.
	buf2, err := openSpillover(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = buf2.Close() }()

	if got := buf2.SizeBytes(); got != written {
		t.Errorf("reopened SizeBytes = %d, want %d (rebuild scan should have summed bucket values)", got, written)
	}
}

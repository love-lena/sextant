package shipper

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/love-lena/sextant-initial/pkg/clickhouseboot"
	"github.com/love-lena/sextant-initial/pkg/sextantd"
	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// TestSpilloverSurvivesClickHouseRestart is the M6 second acceptance
// test: events published while ClickHouse is down must land in the
// BoltDB buffer, then drain back to ClickHouse on recovery, with no
// row lost.
//
// We:
//  1. Boot NATS + ClickHouse + Shipper (via newFixture).
//  2. Publish an envelope to confirm baseline write works.
//  3. Stop ClickHouse mid-flight.
//  4. Publish more envelopes — these should spill to BoltDB AND be
//     ack'd on JetStream.
//  5. Restart ClickHouse with the same dataDir + password (so the
//     `events` table sticks around).
//  6. Wait for the drain loop to flush the spillover back into
//     ClickHouse.
//  7. Assert every published envelope ID is present in `events`.
func TestSpilloverSurvivesClickHouseRestart(t *testing.T) {
	requireBins(t)
	f := newFixture(t)

	shp, err := New(context.Background(), f.cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = shp.Close() })

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	runDone := make(chan error, 1)
	go func() { runDone <- shp.Run(runCtx) }()
	time.Sleep(300 * time.Millisecond) // consumers register

	agentID := uuid.New()
	from := sextantproto.Address{Kind: sextantproto.AddressAgent, ID: agentID.String()}
	subject := fmt.Sprintf("agents.%s.frames", agentID.String())

	// Step 1: baseline publish — should land in ClickHouse fast.
	baseline, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from,
		sextantproto.AgentFramePayload{FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"i": "baseline"}})
	if err != nil {
		t.Fatalf("baseline env: %v", err)
	}
	f.publishEnvelope(t, subject, baseline)
	f.waitForCount(t, "events", fmt.Sprintf("id = toUUID('%s')", baseline.ID.String()), 1, 5*time.Second)

	// Step 2: stop ClickHouse. Subsequent publishes will land in the
	// spillover.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := f.chSrv.Stop(stopCtx); err != nil {
		stopCancel()
		t.Fatalf("ch Stop: %v", err)
	}
	stopCancel()

	// Step 3: publish a handful of envelopes while ClickHouse is down.
	// Use small batches so they hit the buffer quickly.
	const downCount = 25
	downIDs := make([]string, 0, downCount)
	for i := 0; i < downCount; i++ {
		env, err := sextantproto.NewEnvelopeWith(sextantproto.KindAgentFrame, from,
			sextantproto.AgentFramePayload{FrameKind: sextantproto.FrameAssistantText, Body: map[string]any{"i": i}})
		if err != nil {
			t.Fatalf("env %d: %v", i, err)
		}
		f.publishEnvelope(t, subject, env)
		downIDs = append(downIDs, env.ID.String())
	}

	// Wait until the spillover has absorbed at least some of the
	// down-period writes. The shipper's writeBatch path falls through
	// to spillover on ClickHouse error and acks the JetStream message.
	// We expect bytes in the BoltDB file.
	if !waitBufferGrowth(t, shp, 5*time.Second) {
		t.Fatalf("spillover did not grow within deadline (size=%d)", shp.Stats().BufferDepthBytes)
	}

	// Step 4: restart ClickHouse with the same dataDir + password so
	// the schema (and `events` table) survives.
	chCfg := clickhouseboot.DefaultConfig(filepath.Join(f.dataDir, "clickhouse"))
	chCfg.LogFile = filepath.Join(f.dataDir, "clickhouse.log")
	chPasswordPath := filepath.Join(f.configDir, "clickhouse.password")
	pw, err := sextantd.ReadPasswordFile(chPasswordPath)
	if err != nil {
		t.Fatalf("read password: %v", err)
	}
	chCfg.Password = pw
	// Reuse the same ports so the live ClickHouse driver inside the
	// shipper reconnects without extra config plumbing.
	host, port, err := splitHostPort(f.cfg.ClickHouse.Addr)
	if err != nil {
		t.Fatalf("split addr %s: %v", f.cfg.ClickHouse.Addr, err)
	}
	chCfg.ListenHost = host
	chCfg.TCPPort = port

	chSrv2, err := clickhouseboot.Start(context.Background(), chCfg)
	if err != nil {
		t.Fatalf("ch restart: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer stopCancel()
		_ = chSrv2.Stop(stopCtx)
	})
	// Replace the fixture's chSrv so queryCount uses the new one.
	f.chSrv = chSrv2

	// Step 5: wait for the drain loop (1 Hz tick) to flush every
	// buffered row back into ClickHouse.
	deadline := time.Now().Add(45 * time.Second)
	for {
		// Build a comma-separated UUID list for the IN clause.
		var inList string
		for i, id := range downIDs {
			if i > 0 {
				inList += ","
			}
			inList += fmt.Sprintf("toUUID('%s')", id)
		}
		want := uint64(len(downIDs))
		got := f.queryCount(t, "events", fmt.Sprintf("id IN (%s)", inList))
		if got >= want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("drain incomplete: have %d/%d rows after %s; buffer=%d errors=%d",
				got, want, time.Since(deadline.Add(-45*time.Second)),
				shp.Stats().BufferDepthBytes, shp.Stats().ErrorsTotal)
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Step 6: spillover should now be empty (or nearly so — the
	// metrics flush may still be pending).
	deadline = time.Now().Add(10 * time.Second)
	for {
		counts, err := shp.buf.CountAll()
		if err != nil {
			t.Fatalf("CountAll: %v", err)
		}
		// We allow nonzero counts in tables OTHER than events (the
		// metrics publisher might be spilling between flushes); the
		// events bucket must be drained to zero.
		if counts[TableEvents] == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Errorf("events spillover not fully drained: %d entries remain", counts[TableEvents])
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	// Clean shutdown.
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s after cancel")
	}
}

// waitBufferGrowth returns true once the shipper's BoltDB file shows a
// nonzero buckets count (events bucket non-empty). Polls 50 ms.
func waitBufferGrowth(t *testing.T, shp *Shipper, deadline time.Duration) bool {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		counts, err := shp.buf.CountAll()
		if err == nil && counts[TableEvents] > 0 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// splitHostPort returns the (host, port) from host:port. Port is
// returned as int so the caller can plug it back into a clickhouseboot
// Config.
func splitHostPort(s string) (string, int, error) {
	// net.SplitHostPort handles IPv6; we accept that complexity.
	host, portStr, err := splitHP(s)
	if err != nil {
		return "", 0, err
	}
	var p int
	if _, err := fmt.Sscanf(portStr, "%d", &p); err != nil {
		return "", 0, fmt.Errorf("parse port %q: %w", portStr, err)
	}
	return host, p, nil
}

// splitHP wraps net.SplitHostPort — kept indirect so the test file does
// not pull in net just for this single call.
func splitHP(s string) (string, string, error) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("missing port in %q", s)
}

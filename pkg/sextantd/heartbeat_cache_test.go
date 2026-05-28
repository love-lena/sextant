package sextantd

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"

	"github.com/love-lena/sextant/pkg/natsboot"
	"github.com/love-lena/sextant/pkg/sextantproto"
)

// requireNATSBin skips when nats-server is not on PATH.
func requireNATSBin(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("nats-server"); err != nil {
		t.Skipf("nats-server not on PATH: %v", err)
	}
}

// startTestNATS boots a minimal nats-server and returns an open *nats.Conn.
// Both the server and the connection are cleaned up with t.Cleanup.
func startTestNATS(t *testing.T) *nats.Conn {
	t.Helper()
	requireNATSBin(t)

	dataDir := filepath.Join(t.TempDir(), "nats")
	cfg := natsboot.DefaultConfig(dataDir)

	srv, err := natsboot.Start(context.Background(), cfg)
	if err != nil {
		t.Fatalf("natsboot.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})

	nc, err := srv.Connect()
	if err != nil {
		t.Fatalf("srv.Connect: %v", err)
	}
	t.Cleanup(nc.Close)

	return nc
}

// waitFor polls cond until it returns true or timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// publishHeartbeat publishes one heartbeat envelope on
// agents.<agentID>.heartbeat.
func publishHeartbeat(t *testing.T, nc *nats.Conn, agentID uuid.UUID) {
	t.Helper()
	payload, err := json.Marshal(sextantproto.HeartbeatPayload{
		AgentUUID:     agentID,
		IncarnationID: uuid.New(),
		HostID:        "test",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	env := sextantproto.Envelope{Kind: sextantproto.KindHeartbeat, Payload: payload}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal env: %v", err)
	}
	if err := nc.Publish("agents."+agentID.String()+".heartbeat", raw); err != nil {
		t.Fatalf("publish: %v", err)
	}
	_ = nc.Flush()
}

// TestHeartbeatCacheRecordsLastSeen — start a test NATS server, publish a
// heartbeat envelope, assert LastSeen returns the injected fixed time for
// the agent UUID.
func TestHeartbeatCacheRecordsLastSeen(t *testing.T) {
	nc := startTestNATS(t)

	fixed := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	cache, err := NewHeartbeatCache(nc, WithClock(func() time.Time { return fixed }))
	if err != nil {
		t.Fatalf("NewHeartbeatCache: %v", err)
	}
	t.Cleanup(func() { _ = cache.Stop() })

	agentID := uuid.New()
	publishHeartbeat(t, nc, agentID)

	waitFor(t, 2*time.Second, func() bool {
		_, ok := cache.LastSeen(agentID)
		return ok
	})

	got, ok := cache.LastSeen(agentID)
	if !ok {
		t.Fatal("LastSeen returned false after heartbeat was published")
	}
	if !got.Equal(fixed) {
		t.Errorf("LastSeen = %v, want %v", got, fixed)
	}
}

// TestHeartbeatCacheReturnsFalseForUnknownAgent — build a cache with no
// publishes; LastSeen for an unknown UUID must return (zero, false).
func TestHeartbeatCacheReturnsFalseForUnknownAgent(t *testing.T) {
	nc := startTestNATS(t)

	cache, err := NewHeartbeatCache(nc)
	if err != nil {
		t.Fatalf("NewHeartbeatCache: %v", err)
	}
	t.Cleanup(func() { _ = cache.Stop() })

	got, ok := cache.LastSeen(uuid.New())
	if ok {
		t.Errorf("LastSeen returned true for unknown agent, got time %v", got)
	}
	if !got.IsZero() {
		t.Errorf("LastSeen returned non-zero time %v for unknown agent", got)
	}
}

// TestHeartbeatCacheStopIsIdempotent — Stop twice must both return nil.
func TestHeartbeatCacheStopIsIdempotent(t *testing.T) {
	nc := startTestNATS(t)

	cache, err := NewHeartbeatCache(nc)
	if err != nil {
		t.Fatalf("NewHeartbeatCache: %v", err)
	}

	if err := cache.Stop(); err != nil {
		t.Errorf("first Stop() = %v, want nil", err)
	}
	if err := cache.Stop(); err != nil {
		t.Errorf("second Stop() = %v, want nil", err)
	}
}

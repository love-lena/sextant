package sextant

import (
	"strings"
	"testing"
	"time"
)

// TestHeartbeatRoundTrip pins the SDK heartbeat (TASK-126): once connected, the
// SDK beats on a timer; each beat is acked by the bus AND echoed back down the
// dedicated sx.hb.<self> subject, which the echo watcher records. Observing the
// echo proves the whole round-trip — beat sent, bus stamped + echoed, watcher
// saw it — without reaching into the bus.
func TestHeartbeatRoundTrip(t *testing.T) {
	b := startBus(t)
	c, err := Connect(t.Context(), Options{
		URL:               b.ClientURL(),
		CredsPath:         credsPath(t, b, "beater"),
		Logf:              func(string, ...any) {},
		HeartbeatInterval: 50 * time.Millisecond, // fast so the test is quick
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st := c.HeartbeatState()
		if st.LastEchoSeq > 0 && !st.LastEchoAt.IsZero() {
			return // a beat made the full round-trip and the watcher recorded the echo
		}
		time.Sleep(20 * time.Millisecond)
	}
	st := c.HeartbeatState()
	t.Fatalf("no heartbeat echo recorded within 3s: %+v", st)
}

// TestHeartbeatStateSeqAdvances: successive beats carry increasing Seq, so the
// echo watcher's recorded LastEchoSeq advances past 1.
func TestHeartbeatStateSeqAdvances(t *testing.T) {
	b := startBus(t)
	c, err := Connect(t.Context(), Options{
		URL:               b.ClientURL(),
		CredsPath:         credsPath(t, b, "beater-seq"),
		Logf:              func(string, ...any) {},
		HeartbeatInterval: 30 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c.HeartbeatState().LastEchoSeq >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("heartbeat Seq did not advance to >=2: %+v", c.HeartbeatState())
}

// TestHeartbeatGracefulDegradeOnUnknownOp pins the graceful-degrade contract: a
// bus that does not implement clients.heartbeat answers "unknown operation"; the
// SDK must stop beating (log once) and never crash. isUnknownOperation is the
// predicate the loop keys on — exercise it directly so the contract is pinned
// even without an old bus to dial.
func TestHeartbeatGracefulDegradeOnUnknownOp(t *testing.T) {
	if !isUnknownOperation(&busError{op: "clients.heartbeat", msg: `bus: unknown operation "clients.heartbeat"`}) {
		t.Error("an unknown-operation busError must be recognized as graceful-degrade")
	}
	// A different bus error (e.g. a transient refusal) is NOT graceful-degrade:
	// the loop keeps beating.
	if isUnknownOperation(&busError{op: "clients.heartbeat", msg: "bus: clients.heartbeat: persist last_seen: boom"}) {
		t.Error("a non-unknown-op error must not be treated as graceful-degrade")
	}
	// A nil or transport error is not a definitive unknown-op either.
	if isUnknownOperation(nil) {
		t.Error("nil must not be treated as unknown-op")
	}
}

// TestHeartbeatStopsBeatingOnUnknownOp: when the bus replies unknown-operation,
// the SDK stops beating after logging exactly once — it does not spin. Drive it
// by pointing the loop at a bus that has the op removed is not possible here, so
// instead assert the loop exits on the first unknown-op via the unexported
// runHeartbeat against a stub call.
func TestHeartbeatStopsBeatingOnUnknownOp(t *testing.T) {
	var logged []string
	c := &Client{
		logf:   func(f string, a ...any) { logged = append(logged, f) },
		closed: make(chan struct{}),
	}
	calls := 0
	stubBeat := func() error {
		calls++
		return &busError{op: "clients.heartbeat", msg: `bus: unknown operation "clients.heartbeat"`}
	}
	// A short interval; the loop must exit after the first unknown-op reply.
	c.runHeartbeatLoop(1*time.Millisecond, stubBeat)
	if calls != 1 {
		t.Errorf("loop should stop after the first unknown-op reply, made %d calls", calls)
	}
	var sawOnce bool
	for _, l := range logged {
		if strings.Contains(l, "heartbeat") {
			sawOnce = true
		}
	}
	if !sawOnce {
		t.Errorf("stopping on unknown-op should log once; logs = %v", logged)
	}
}

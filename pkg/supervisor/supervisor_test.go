package supervisor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeProc is a Process implementation driven by test code.
type fakeProc struct {
	waitCh chan error
	stop   func(ctx context.Context) error
}

func newFakeProc(exitErr error) *fakeProc {
	p := &fakeProc{waitCh: make(chan error, 1)}
	p.waitCh <- exitErr
	close(p.waitCh)
	return p
}

func (p *fakeProc) Wait() error {
	return <-p.waitCh
}

func (p *fakeProc) Stop(ctx context.Context) error {
	if p.stop != nil {
		return p.stop(ctx)
	}
	return nil
}

func drain(t *testing.T, ch <-chan Event) []Event {
	t.Helper()
	var out []Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

// TestQuarantineAfterRepeatedFailures asserts the supervisor stops
// restarting once QuarantineAfter consecutive failures are seen.
func TestQuarantineAfterRepeatedFailures(t *testing.T) {
	var starts atomic.Int32
	sleepCalls := atomic.Int32{}

	u := Unit{
		Name: "test",
		Policy: Policy{
			InitialBackoff:  10 * time.Millisecond,
			MaxBackoff:      40 * time.Millisecond,
			QuarantineAfter: 3,
			ResetAfter:      time.Hour, // never reset during the test
		},
		Start: func(_ context.Context) (Process, error) {
			starts.Add(1)
			return newFakeProc(errors.New("boom")), nil
		},
		sleep: func(_ context.Context, _ time.Duration) error {
			sleepCalls.Add(1)
			return nil
		},
	}
	s, err := New(u)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(context.Background()) }()

	evs := drain(t, s.Events())

	err = <-runErr
	if err == nil {
		t.Fatal("expected Run to return quarantine error")
	}
	if !s.Quarantined() {
		t.Fatal("expected quarantined state")
	}
	if got := starts.Load(); got != 3 {
		t.Fatalf("starts = %d, want 3", got)
	}

	// Event tape: started, exited, restarting, started, exited, restarting, started, exited, quarantined.
	wantSeq := []EventKind{
		EventStarted, EventExited, EventRestarting,
		EventStarted, EventExited, EventRestarting,
		EventStarted, EventExited, EventQuarantined,
	}
	if len(evs) != len(wantSeq) {
		t.Fatalf("got %d events, want %d: %#v", len(evs), len(wantSeq), evs)
	}
	for i, want := range wantSeq {
		if evs[i].Kind != want {
			t.Errorf("event %d: kind=%s, want %s", i, evs[i].Kind, want)
		}
	}
}

// TestBackoffGrowsExponentially asserts each retry's sleep doubles until
// the cap. We capture the supervised sleep durations via the sleep hook.
func TestBackoffGrowsExponentially(t *testing.T) {
	var waits []time.Duration
	u := Unit{
		Name: "test",
		Policy: Policy{
			InitialBackoff:  10 * time.Millisecond,
			MaxBackoff:      50 * time.Millisecond,
			QuarantineAfter: 6,
			ResetAfter:      time.Hour,
		},
		Start: func(_ context.Context) (Process, error) {
			return newFakeProc(errors.New("boom")), nil
		},
		sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		},
	}
	s, err := New(u)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go func() { _ = s.Run(context.Background()) }()
	for range s.Events() {
		// consume all events to keep supervisor unblocked
	}

	// 6 failures → 5 sleeps before quarantine.
	if len(waits) != 5 {
		t.Fatalf("waits = %v, want 5 entries", waits)
	}
	want := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond, // capped
		50 * time.Millisecond,
	}
	for i, w := range want {
		if waits[i] != w {
			t.Errorf("wait[%d] = %s, want %s", i, waits[i], w)
		}
	}
}

// TestStopReturnsCleanly asserts that calling Stop after Run is in
// progress terminates the supervisor without quarantining.
func TestStopReturnsCleanly(t *testing.T) {
	procDoneCh := make(chan struct{})
	u := Unit{
		Name: "test",
		Policy: Policy{
			InitialBackoff:  1 * time.Millisecond,
			MaxBackoff:      1 * time.Millisecond,
			QuarantineAfter: 100,
			ResetAfter:      time.Hour,
		},
		Start: func(_ context.Context) (Process, error) {
			p := &fakeProc{waitCh: make(chan error, 1)}
			p.stop = func(_ context.Context) error {
				p.waitCh <- nil
				close(p.waitCh)
				close(procDoneCh)
				return nil
			}
			return p, nil
		},
	}
	s, err := New(u)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(context.Background()) }()

	// Drain events in the background.
	go func() {
		for range s.Events() {
		}
	}()

	// Wait for the started event by polling.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		started := s.current != nil
		s.mu.Unlock()
		if started {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s")
	}
	if s.Quarantined() {
		t.Fatal("Quarantined should be false on clean stop")
	}
}

// TestResetAfterClearsBackoff exercises the "stayed up long enough" reset.
func TestResetAfterClearsBackoff(t *testing.T) {
	var nowVal atomic.Int64
	nowVal.Store(time.Now().UnixNano())

	step := func(d time.Duration) {
		nowVal.Add(int64(d))
	}

	var waits []time.Duration
	var startCount atomic.Int32
	u := Unit{
		Name: "test",
		Policy: Policy{
			InitialBackoff:  10 * time.Millisecond,
			MaxBackoff:      80 * time.Millisecond,
			QuarantineAfter: 10,
			ResetAfter:      100 * time.Millisecond,
		},
		Start: func(_ context.Context) (Process, error) {
			n := startCount.Add(1)
			p := &fakeProc{waitCh: make(chan error, 1)}
			// The 3rd start gets a "long" uptime so the reset path fires.
			if n == 3 {
				go func() {
					step(200 * time.Millisecond)
					p.waitCh <- errors.New("boom")
					close(p.waitCh)
				}()
			} else {
				p.waitCh <- errors.New("boom")
				close(p.waitCh)
			}
			return p, nil
		},
		sleep: func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		},
		now: func() time.Time { return time.Unix(0, nowVal.Load()) },
	}
	s, err := New(u)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Stop after collecting 5 starts.
	go func() {
		for ev := range s.Events() {
			if ev.Kind == EventStarted && startCount.Load() >= 5 {
				_ = s.Stop(context.Background())
				// drain the rest
				for range s.Events() {
				}
				return
			}
		}
	}()
	_ = s.Run(context.Background())
	if len(waits) < 3 {
		t.Fatalf("not enough waits captured: %v", waits)
	}
	// After the 3rd failure (which stayed up 200ms > ResetAfter=100ms),
	// the backoff should reset to InitialBackoff.
	if waits[2] != 10*time.Millisecond {
		t.Errorf("wait[2] = %s, want reset to 10ms", waits[2])
	}
}

// TestStopDuringRestartGracefullyStopsNewProc closes the race window
// where Stop arrives between Start() returning a new Process and the
// supervisor installing it as current. Pre-fix, the freshly-started
// process was left running and only died on ctx-cancel SIGKILL — bad
// for any unit that needs a graceful Stop (e.g. JetStream flush). The
// fix is the atomic stopped-check inside installCurrent; this test
// drives the exact window.
func TestStopDuringRestartGracefullyStopsNewProc(t *testing.T) {
	// startGate releases the unit's Start function after Stop has been
	// observed. release closes once we want the goroutine to proceed.
	startGate := make(chan struct{})
	stopCalled := make(chan struct{})

	u := Unit{
		Name: "test",
		Policy: Policy{
			InitialBackoff:  1 * time.Millisecond,
			MaxBackoff:      1 * time.Millisecond,
			QuarantineAfter: 100,
			ResetAfter:      time.Hour,
		},
		Start: func(_ context.Context) (Process, error) {
			// Block until the test signals "Stop has been called".
			<-startGate
			p := &fakeProc{waitCh: make(chan error, 1)}
			p.stop = func(_ context.Context) error {
				select {
				case <-stopCalled:
				default:
					close(stopCalled)
				}
				p.waitCh <- nil
				close(p.waitCh)
				return nil
			}
			return p, nil
		},
	}
	s, err := New(u)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(context.Background()) }()
	go func() {
		for range s.Events() {
		}
	}()

	// Wait until Start is parked on startGate — the supervisor is now
	// inside Start, between the entry check and the (would-be)
	// setCurrent. This is the race window we're testing.
	time.Sleep(50 * time.Millisecond)

	// Call Stop. Pre-fix: Stop sees current==nil and returns; the
	// freshly-built proc starts and blocks on Wait() forever.
	// Post-fix: Stop sets stopped=true; installCurrent observes it and
	// hands the new proc to the caller, which Stops it.
	stopReturned := make(chan error, 1)
	go func() { stopReturned <- s.Stop(context.Background()) }()

	// Release Start so it returns a fresh Process.
	close(startGate)

	// The unit's Stop must be called within a bounded time.
	select {
	case <-stopCalled:
	case <-time.After(2 * time.Second):
		t.Fatalf("unit Stop was never called — race window still open")
	}

	// And the supervisor's Run must return cleanly (no quarantine).
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after stop-during-restart")
	}

	// Stop() itself returned.
	select {
	case err := <-stopReturned:
		if err != nil {
			t.Errorf("Stop returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return")
	}

	if s.Quarantined() {
		t.Error("Quarantined should be false on stop-during-restart")
	}
}

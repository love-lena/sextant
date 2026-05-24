package supervisor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Process is the minimal interface a supervised unit must implement.
// Wait blocks until the process exits and returns a nil-or-non-nil exit
// error; Stop politely asks the process to exit (graceful shutdown is the
// caller's responsibility).
type Process interface {
	// Wait blocks until the underlying process has exited.
	Wait() error
	// Stop signals the process to terminate. The supervisor calls Stop
	// during shutdown; supervised units should treat the supplied ctx as
	// a graceful-shutdown budget.
	Stop(ctx context.Context) error
}

// StartFn brings up a new instance of the supervised unit and returns a
// Process handle. It is invoked once on Start and again on every
// restart. The ctx supplied to StartFn is the supervisor's lifetime
// context — once it is canceled, restarts stop.
type StartFn func(ctx context.Context) (Process, error)

// Event is one observable transition in a unit's life. The supervisor
// emits events on its event channel so callers can audit and emit
// telemetry without coupling to the supervisor's internal state.
type Event struct {
	Name  string    // unit name
	Kind  EventKind // started | exited | restarting | quarantined | stopped
	Err   error     // populated for exited / quarantined
	At    time.Time // wall-clock event time
	Wait  time.Duration
	Tries int // 1-based restart attempt counter at the time of the event
}

// EventKind enumerates supervisor.Event.Kind.
type EventKind string

const (
	EventStarted     EventKind = "started"
	EventExited      EventKind = "exited"
	EventRestarting  EventKind = "restarting"
	EventQuarantined EventKind = "quarantined"
	EventStopped     EventKind = "stopped"
)

// Policy parameterizes the backoff and quarantine behavior.
type Policy struct {
	// InitialBackoff is the first wait between restarts.
	InitialBackoff time.Duration
	// MaxBackoff caps the exponential growth.
	MaxBackoff time.Duration
	// QuarantineAfter is the number of consecutive restart failures
	// before the supervisor stops auto-restarting.
	QuarantineAfter int
	// ResetAfter is the continuous-uptime window after which the
	// restart counter resets to zero.
	ResetAfter time.Duration
}

// DefaultPolicy returns sextant's default supervision parameters.
func DefaultPolicy() Policy {
	return Policy{
		InitialBackoff:  1 * time.Second,
		MaxBackoff:      5 * time.Minute,
		QuarantineAfter: 5,
		ResetAfter:      5 * time.Minute,
	}
}

// Unit is one supervised process.
type Unit struct {
	Name   string
	Start  StartFn
	Policy Policy

	// optional clock + sleep hooks for tests. Zero values mean
	// "use the real wall clock".
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// Supervisor runs a single Unit. Build with New, then call Start which
// blocks until Stop is invoked or the unit is quarantined.
type Supervisor struct {
	unit Unit

	mu          sync.Mutex
	current     Process
	quarantined bool
	stopped     bool

	events chan Event
}

// New returns a Supervisor for the given unit. The Unit must have a name
// and a non-nil Start; Policy fields default if zero.
func New(unit Unit) (*Supervisor, error) {
	if unit.Name == "" {
		return nil, fmt.Errorf("supervisor: unit Name required")
	}
	if unit.Start == nil {
		return nil, fmt.Errorf("supervisor: unit Start required")
	}
	p := unit.Policy
	if p.InitialBackoff <= 0 {
		p.InitialBackoff = 1 * time.Second
	}
	if p.MaxBackoff <= 0 {
		p.MaxBackoff = 5 * time.Minute
	}
	if p.QuarantineAfter <= 0 {
		p.QuarantineAfter = 5
	}
	if p.ResetAfter <= 0 {
		p.ResetAfter = 5 * time.Minute
	}
	unit.Policy = p
	if unit.now == nil {
		unit.now = time.Now
	}
	if unit.sleep == nil {
		unit.sleep = defaultSleep
	}
	return &Supervisor{
		unit:   unit,
		events: make(chan Event, 16),
	}, nil
}

// Events returns the supervisor's event channel. The channel closes when
// Run returns. Drain it from a separate goroutine; otherwise the
// supervisor blocks on event emission.
func (s *Supervisor) Events() <-chan Event { return s.events }

// Quarantined reports whether the supervisor has stopped auto-restarting
// due to too many consecutive failures.
func (s *Supervisor) Quarantined() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.quarantined
}

// Run starts the unit and blocks until ctx is canceled, Stop is called,
// or the unit is quarantined. Returns nil on graceful stop, an error on
// quarantine, or ctx.Err() if the context was canceled.
func (s *Supervisor) Run(ctx context.Context) error {
	defer close(s.events)

	tries := 0
	backoff := s.unit.Policy.InitialBackoff

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.isStopped() {
			s.emit(Event{Name: s.unit.Name, Kind: EventStopped, At: s.unit.now()})
			return nil
		}

		startedAt := s.unit.now()
		proc, err := s.unit.Start(ctx)
		if err != nil {
			tries++
			s.emit(Event{Name: s.unit.Name, Kind: EventExited, Err: err, At: s.unit.now(), Tries: tries})
			if tries >= s.unit.Policy.QuarantineAfter {
				s.markQuarantined()
				s.emit(Event{Name: s.unit.Name, Kind: EventQuarantined, Err: err, At: s.unit.now(), Tries: tries})
				return fmt.Errorf("supervisor: %s quarantined after %d failures: %w", s.unit.Name, tries, err)
			}
			s.emit(Event{Name: s.unit.Name, Kind: EventRestarting, At: s.unit.now(), Wait: backoff, Tries: tries})
			if err := s.unit.sleep(ctx, backoff); err != nil {
				return err
			}
			backoff = nextBackoff(backoff, s.unit.Policy.MaxBackoff)
			continue
		}

		// Race window: Stop() may have observed current==nil while
		// Start() was running, set stopped=true, and returned. Without
		// this guard the freshly-started subprocess would only die when
		// the supervisor's ctx is canceled — by then exec.CommandContext
		// uses SIGKILL, skipping the unit's graceful Stop path. Doing
		// the set-or-stop under one lock closes the window.
		if !s.installCurrent(proc) {
			s.emit(Event{Name: s.unit.Name, Kind: EventStopped, At: s.unit.now()})
			// Best-effort graceful stop of the just-started unit. We
			// still ignore the returned error here: Run's contract is
			// "return nil on clean stop" and the caller already learned
			// the supervisor was asked to stop.
			_ = proc.Stop(ctx)
			return nil
		}
		s.emit(Event{Name: s.unit.Name, Kind: EventStarted, At: startedAt})

		waitErr := proc.Wait()
		uptime := s.unit.now().Sub(startedAt)
		s.setCurrent(nil)

		// Graceful path: Stop was called, Wait returned (any error or
		// nil) — treat as clean shutdown.
		if s.isStopped() {
			s.emit(Event{Name: s.unit.Name, Kind: EventStopped, At: s.unit.now()})
			return nil
		}

		tries++
		s.emit(Event{Name: s.unit.Name, Kind: EventExited, Err: waitErr, At: s.unit.now(), Tries: tries})

		// Reset counters if the unit stayed up long enough.
		if uptime >= s.unit.Policy.ResetAfter {
			tries = 1
			backoff = s.unit.Policy.InitialBackoff
		}

		if tries >= s.unit.Policy.QuarantineAfter {
			s.markQuarantined()
			s.emit(Event{Name: s.unit.Name, Kind: EventQuarantined, Err: waitErr, At: s.unit.now(), Tries: tries})
			return fmt.Errorf("supervisor: %s quarantined after %d failures: %w", s.unit.Name, tries, errOrNil(waitErr))
		}

		s.emit(Event{Name: s.unit.Name, Kind: EventRestarting, At: s.unit.now(), Wait: backoff, Tries: tries})
		if err := s.unit.sleep(ctx, backoff); err != nil {
			return err
		}
		backoff = nextBackoff(backoff, s.unit.Policy.MaxBackoff)
	}
}

// Stop signals the supervisor to terminate the underlying unit and
// return from Run. It is safe to call Stop multiple times.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	current := s.current
	s.mu.Unlock()

	if current == nil {
		return nil
	}
	return current.Stop(ctx)
}

func (s *Supervisor) isStopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

func (s *Supervisor) setCurrent(p Process) {
	s.mu.Lock()
	s.current = p
	s.mu.Unlock()
}

// installCurrent atomically stores proc as the current process unless
// Stop has already been observed. Returns true if proc was installed;
// false means Stop fired during Start() and the caller is responsible
// for tearing proc down gracefully.
func (s *Supervisor) installCurrent(p Process) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return false
	}
	s.current = p
	return true
}

func (s *Supervisor) markQuarantined() {
	s.mu.Lock()
	s.quarantined = true
	s.mu.Unlock()
}

func (s *Supervisor) emit(e Event) {
	if e.At.IsZero() {
		e.At = s.unit.now()
	}
	select {
	case s.events <- e:
	default:
		// channel full — drop. Supervisor must never block on an
		// unattended event channel; observability is best-effort.
	}
}

func nextBackoff(current, max time.Duration) time.Duration {
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func errOrNil(err error) error {
	if err == nil {
		return errors.New("process exited cleanly")
	}
	return err
}

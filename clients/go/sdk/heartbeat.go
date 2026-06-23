package sextant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/love-lena/sextant/protocol/wireapi"
	"github.com/nats-io/nats.go"
)

// HeartbeatState is the SDK's in-process view of its own liveness round-trip
// (TASK-126): the last beat sent and the last echo the bus pushed back on the
// dedicated sx.hb.<self> subject. It is for a future watchdog (TASK-124) to
// consume — a beat sent but not echoed within the freshness window is a stale
// push path. The echo itself is internal; it is never surfaced to the user as a
// message. Read with HeartbeatState; the zero value means no echo has arrived.
type HeartbeatState struct {
	// LastBeatSeq is the highest beat sequence the SDK has sent.
	LastBeatSeq uint64
	// LastEchoSeq is the sequence of the most recent echo recorded by the watcher.
	LastEchoSeq uint64
	// LastEchoAt is when that echo arrived (local clock). Zero until the first echo.
	LastEchoAt time.Time
	// Fresh reports whether the last echo is within the SDK's freshness window —
	// the watchdog's "push path is live" signal. False when no echo has arrived.
	Fresh bool
}

// HeartbeatState returns a snapshot of the heartbeat round-trip state. It never
// blocks; it reads the locally recorded values under a short lock.
func (c *Client) HeartbeatState() HeartbeatState {
	c.hbMu.Lock()
	echoSeq, echoAt := c.hbLastEchoSeq, c.hbLastEchoAt
	c.hbMu.Unlock()
	fresh := !echoAt.IsZero() && time.Since(echoAt) < c.hbFreshness
	return HeartbeatState{
		LastBeatSeq: c.hbSeq.Load(),
		LastEchoSeq: echoSeq,
		LastEchoAt:  echoAt,
		Fresh:       fresh,
	}
}

// startHeartbeat wires the echo watcher and starts the beat loop (TASK-126).
// Called once at the end of Connect, after the inbox subscription and the hello
// handshake, so the watcher's subscription is registered before any beat can
// produce an echo. The watcher is a plain core-NATS subscription on the client's
// own sx.hb.<self> subject — not a JetStream relay — matching the bus's transient
// core publish; a missed echo means a dead path, never a queued backlog. The beat
// loop runs on its own goroutine until Close signals via c.closed.
func (c *Client) startHeartbeat(ctx context.Context, interval time.Duration) error {
	sub, err := c.nc.Subscribe(wireapi.HeartbeatSubject(c.id), func(m *nats.Msg) {
		var echo wireapi.HeartbeatEcho
		if err := json.Unmarshal(m.Data, &echo); err != nil {
			c.logf("sextant: undecodable heartbeat echo on %s: %v", wireapi.HeartbeatSubject(c.id), err)
			return
		}
		c.recordEcho(echo.Seq)
	})
	if err != nil {
		return fmt.Errorf("sextant: subscribe heartbeat echo: %w", err)
	}
	c.hbEchoSub = sub
	// Flush so the echo subscription is registered server-side before the first
	// beat, honoring the caller's deadline when set (mirrors watchDrain).
	flush := c.nc.Flush
	if _, ok := ctx.Deadline(); ok {
		flush = func() error { return c.nc.FlushWithContext(ctx) }
	}
	if err := flush(); err != nil {
		return fmt.Errorf("sextant: flush heartbeat echo subscription: %w", err)
	}

	go c.runHeartbeatLoop(interval, c.sendBeat)
	return nil
}

// sendBeat sends one clients.heartbeat carrying the next monotonic Seq. It is the
// production beat function passed to runHeartbeatLoop; the loop owns the cadence
// and the stop conditions. Each call uses a short per-beat deadline so a wedged
// bus cannot stall the loop indefinitely (fail-loud, fail-early).
func (c *Client) sendBeat() error {
	seq := c.hbSeq.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.call(ctx, wireapi.OpClientsHeartbeat, wireapi.HeartbeatInput{Seq: seq}, nil)
}

// runHeartbeatLoop ticks every interval and calls beat, until Close signals via
// c.closed. It implements the graceful-degrade contract (TASK-126): if a beat
// comes back "unknown operation" (an older bus that does not implement the op),
// it logs once and returns — never crashing, leaving presence to the connection
// table. Any other beat error (a transient refusal, a transport timeout) is
// logged and the loop keeps beating: a single failed beat is not fatal, and a
// later beat refreshes last_seen. beat is a seam so a unit test can drive the
// loop without a live bus.
func (c *Client) runHeartbeatLoop(interval time.Duration, beat func() error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			if err := beat(); err != nil {
				if isUnknownOperation(err) {
					c.logf("sextant: bus does not implement clients.heartbeat; stopping heartbeats (presence falls back to the connection table)")
					return
				}
				c.logf("sextant: heartbeat failed (will retry next interval): %v", err)
			}
		}
	}
}

// recordEcho stores the most recent echo seq and arrival time (the single writer
// path for the watcher-recorded state).
func (c *Client) recordEcho(seq uint64) {
	c.hbMu.Lock()
	c.hbLastEchoSeq = seq
	c.hbLastEchoAt = time.Now()
	c.hbMu.Unlock()
}

// isUnknownOperation reports whether err is the bus's definitive "this operation
// does not exist" reply — the graceful-degrade signal for an older bus that
// predates clients.heartbeat (TASK-126). It keys on a busError (the bus answered)
// whose message names the unknown-operation refusal the dispatch default arm
// emits ("bus: unknown operation \"...\""), never on a transport error (where the
// bus never answered and the op may well exist). A transient bus-side failure
// from a bus that DOES have the op is not unknown-operation, so the loop keeps
// beating.
func isUnknownOperation(err error) bool {
	if err == nil {
		return false
	}
	var be *busError
	if !errors.As(err, &be) {
		return false // transport failure: the bus never answered; do not assume absence
	}
	return strings.Contains(be.msg, "unknown operation")
}

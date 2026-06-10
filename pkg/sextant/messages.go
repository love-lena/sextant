package sextant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/nats.go"
	"github.com/oklog/ulid/v2"
)

// Message is a received message: the decoded frame plus the bus-stamped
// metadata the receiver trusts.
type Message struct {
	Frame    wire.Frame
	Subject  string
	BusTime  time.Time // JetStream-stamped; the trusted clock
	Sequence uint64
}

// Handler processes a received message.
type Handler func(Message)

// Subscription is an active subscription; call Stop to end it.
type Subscription interface {
	Stop()
}

type subConfig struct {
	deliverAll bool
	onErr      func(error)
}

// SubOption configures Subscribe.
type SubOption func(*subConfig)

// DeliverAll replays the full backlog on the subject before live messages.
// Without it, a subscription delivers only messages published after it starts.
func DeliverAll() SubOption {
	return func(c *subConfig) { c.deliverAll = true }
}

// OnError registers a handler that is called when the subscription cannot be
// re-established after a reconnect. A subscription that was live when the
// connection dropped will attempt to resume on reconnect; if that fails (e.g.
// the store was wiped) the handler is called with the error and the
// subscription is stopped. Without OnError, a failed resume is only logged —
// the handler receives nothing, forever. Registering an OnError makes that
// silence visible.
//
// The handler runs on the NATS client's asynchronous-callback goroutine, the
// same one that dispatches reconnect events: it must not block, and it must not
// make calls on this client (they would deadlock or time out behind the
// reconnect in progress). Hand the error off to a channel and return.
func OnError(h func(error)) SubOption {
	return func(c *subConfig) { c.onErr = h }
}

// Publish sends record to subject, which must be in the messages space (msg.*),
// as a message.publish call: the bus stamps the frame (id, author, epoch) and
// appends it to the durable log, replying once the log has it (ADR-0019). The
// client supplies only the subject and record.
func (c *Client) Publish(ctx context.Context, subject string, record json.RawMessage) error {
	if !strings.HasPrefix(subject, sx.MessagePrefix) {
		return fmt.Errorf("sextant: publish subject %q is not in the messages space (%s*)", subject, sx.MessagePrefix)
	}
	return c.call(ctx, wireapi.OpMessagePublish, wireapi.PublishInput{Subject: subject, Record: record}, nil)
}

// FetchMessages pulls a batch of retained messages on subject (an exact subject
// or a wildcard) from the cursor since (0 = the start of retained history) as a
// message.read call. It returns the bus-stamped frames and the cursor to resume
// from; passing next unchanged to the following call yields no gaps and no
// duplicates. It is the pull complement to Subscribe.
func (c *Client) FetchMessages(ctx context.Context, subject string, since uint64, limit int) (frames []wire.Frame, next uint64, err error) {
	var out wireapi.ReadOutput
	if err := c.call(ctx, wireapi.OpMessageRead, wireapi.ReadInput{Subject: subject, Since: since, Limit: limit}, &out); err != nil {
		return nil, since, err
	}
	return out.Messages, out.NextCursor, nil
}

// Subscribe delivers messages matching subject (an exact subject or a wildcard,
// e.g. sx.TopicSubject("plan") or "msg.>") to h as a message.subscribe call: the
// bus relays matching frames to this client's private delivery subject
// (sx.deliver.<id>.<sub>), and the SDK fans them out to h (ADR-0019). Replay is
// client-controlled (see DeliverAll); the bus owns the cursor, so it keeps no
// per-subscriber state beyond the live relay. Each delivered frame is re-checked
// against the wire contract (structure, epoch) and the bus clock, and quarantined
// (skipped + logged) on a violation (ADR-0006, ADR-0010). The subscription runs
// until Stop is called or ctx is cancelled, whichever comes first.
//
// A Subscription survives a reconnect — a bus restart of the same store or a
// plain network blip (ADR-0027): on reconnect the SDK re-establishes the
// server-side relay, resuming from the last delivered sequence so no messages
// are missed or duplicated. If re-establishment is impossible (e.g. the store
// was wiped), the OnError handler is called and the subscription is stopped —
// never silent.
func (c *Client) Subscribe(ctx context.Context, subject string, h Handler, opts ...SubOption) (Subscription, error) {
	var cfg subConfig
	for _, o := range opts {
		o(&cfg)
	}
	subID := ulid.Make().String()
	deliver := wireapi.DeliverSubject(c.id, subID)

	s := &subscription{
		c:          c,
		subID:      subID,
		subject:    subject,
		deliverAll: cfg.deliverAll,
		handler:    h,
		onErr:      cfg.onErr,
	}

	// Subscribe to the delivery subject BEFORE making the call, so a frame the bus
	// relays the instant it replies can't outrun our subscription.
	natsSub, err := c.nc.Subscribe(deliver, c.relayHandler(s, c.nc.Stats().Reconnects))
	if err != nil {
		return nil, fmt.Errorf("sextant: subscribe delivery: %w", err)
	}
	s.natsSub = natsSub

	if err := c.call(ctx, wireapi.OpMessageSubscribe, wireapi.SubscribeInput{
		Subject:    subject,
		SubID:      subID,
		DeliverAll: cfg.deliverAll,
	}, nil); err != nil {
		_ = natsSub.Unsubscribe()
		return nil, err
	}

	subCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Register so the reconnect handler can find this subscription.
	c.registerSub(s)

	// Bridge ctx cancellation to teardown: a cancelled ctx ends the subscription
	// (unsubscribe + bus-side relay stop), same as an explicit Stop. The bridge
	// also unblocks on Stop (which cancels subCtx), so it never leaks.
	go func() {
		<-subCtx.Done()
		s.teardown()
		c.deregisterSub(s)
	}()
	return s, nil
}

// relayHandler returns the delivery-subject handler for one relay generation —
// the (sub-id, server-side relay) pair established by Subscribe or by one
// resume pass. epoch is the connection's reconnect count when the generation
// was established: a frame processed after a later reconnect is dropped,
// because its relay may have published part of its stream into the dropped
// connection — a void the client cannot see — so nothing it pushes after a
// reconnect can be trusted to be gap-free. The reconnect's resume pass replaces
// the generation and replays everything past lastSeq, so a dropped frame is
// re-covered, never lost. The NATS client bumps the counter under the
// connection lock before it re-sends subscriptions, so no frame can be
// processed on a restored connection while the count still reads stale.
func (c *Client) relayHandler(s *subscription, epoch uint64) nats.MsgHandler {
	return func(m *nats.Msg) {
		if c.nc.Stats().Reconnects != epoch {
			return // doomed generation: a reconnect intervened; the resume re-covers it
		}
		var d wireapi.MessageDelivery
		if err := json.Unmarshal(m.Data, &d); err != nil {
			c.logf("sextant: undecodable delivery on %s, skipping: %v", s.subject, err)
			return
		}
		c.deliver(d, s.handler, s)
	}
}

// deliver applies the receiver-side quarantine to one pushed delivery and, if it
// passes, hands it to the handler. The check mirrors the pull path: a replayed
// frame (DeliverAll) can carry a prior epoch, and — until the per-client
// allow-list lands — a client can still raw-publish to msg.>, so the SDK never
// trusts that a delivered frame is well-formed.
func (c *Client) deliver(d wireapi.MessageDelivery, h Handler, s *subscription) {
	frame := d.Frame
	if err := frame.Validate(); err != nil {
		c.logf("sextant: quarantined malformed frame on %s: %v", d.Subject, err)
		return
	}
	if err := wire.CheckEpoch(frame.Epoch, wire.Epoch); err != nil {
		c.logf("sextant: quarantined %s on %s: %v", frame.ID, d.Subject, err)
		return
	}
	if err := wire.CheckSkew(frame.ID, d.BusTime, c.skewTol); err != nil {
		c.logf("sextant: quarantined %s on %s: %v", frame.ID, d.Subject, err)
		return
	}
	// Advance the resume cursor and drop overlap. Within one subscription a
	// single ordered relay delivers strictly increasing stream sequences, and a
	// resume relay replays only from last+1 — so a non-increasing sequence is
	// always overlap (a replaced relay's in-flight pushes interleaving with the
	// new relay's replay around a reconnect), never a fresh message. Dropping it
	// holds ADR-0027's no-duplicates guarantee, and the monotonic cursor keeps a
	// stale push from moving the next resume point backwards.
	if s != nil && d.Seq > 0 && !s.advanceLastSeq(d.Seq) {
		return
	}
	h(Message{
		Frame:    frame,
		Subject:  d.Subject,
		BusTime:  d.BusTime,
		Sequence: d.Seq,
	})
}

// reestablishSubs is called by the ReconnectHandler on every successful
// reconnect. It iterates all registered active subscriptions and re-creates
// their server-side relay, resuming from last-delivered+1 so no messages are
// missed or duplicated. A subscription that had no deliveries resumes from its
// original start option. If re-establishment fails for a subscription, the
// OnError handler is called (if registered) and the subscription is stopped.
//
// Ownership: runs on the NATS client's reconnect goroutine. It holds c.subsMu
// only long enough to snapshot the active set; a subscription stopped during
// (or just before) the loop is skipped via its stopped flag.
func (c *Client) reestablishSubs() {
	c.subsMu.Lock()
	// Snapshot active subs so we don't hold the lock while making network calls.
	active := make([]*subscription, 0, len(c.subs))
	for s := range c.subs {
		active = append(active, s)
	}
	c.subsMu.Unlock()

	for _, s := range active {
		if s.stopped.Load() {
			continue // Stop won the race; nothing to re-establish
		}
		if err := s.reestablish(c); err != nil {
			if s.stopped.Load() {
				continue // stopped concurrently; the failure is teardown, not loss
			}
			c.logf("sextant: subscription on %q cannot resume after reconnect: %v", s.subject, err)
			if s.onErr != nil {
				s.onErr(fmt.Errorf("sextant: subscription on %q lost after reconnect: %w", s.subject, err))
			}
			s.cancel() // tears down via the bridge goroutine
			continue
		}
		if s.stopped.Load() {
			// Stop ran while we were re-establishing: the fresh relay may have been
			// registered after teardown's subscription.stop. Stop it again
			// (idempotent on the bus) so no relay is orphaned.
			s.mu.Lock()
			subID := s.subID
			s.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.call(ctx, wireapi.OpSubscriptionStop, wireapi.SubscriptionStopInput{SubID: subID}, nil)
			cancel()
		}
	}
}

// registerSub adds a subscription to the client's active set. The set is keyed
// by the subscription itself, not its sub-id — the sub-id rotates on every
// resume pass, while the subscription is the stable identity.
// Ownership: c.subsMu guards c.subs.
func (c *Client) registerSub(s *subscription) {
	c.subsMu.Lock()
	if c.subs == nil {
		c.subs = make(map[*subscription]struct{})
	}
	c.subs[s] = struct{}{}
	c.subsMu.Unlock()
}

// deregisterSub removes a subscription from the client's active set.
func (c *Client) deregisterSub(s *subscription) {
	c.subsMu.Lock()
	delete(c.subs, s)
	c.subsMu.Unlock()
}

type subscription struct {
	c          *Client
	subject    string
	deliverAll bool
	handler    Handler
	cancel     context.CancelFunc
	onErr      func(error)
	once       sync.Once

	// mu guards the live relay generation: the (subID, natsSub) pair. Every
	// resume pass rotates to a fresh sub-id (see reestablish), so the pair
	// changes over the subscription's life; teardown and the resume passes read
	// the newest pair under the lock.
	mu      sync.Mutex
	subID   string
	natsSub *nats.Subscription

	// stopped is set synchronously in Stop (before cancel) and in teardown, so
	// the reconnect path can observe an in-flight stop and skip — or undo — a
	// re-establish that would otherwise orphan a relay on the bus.
	stopped atomic.Bool

	// lastSeq is the stream sequence of the last message successfully passed to
	// the handler. It only moves forward (see advanceLastSeq) and is read
	// atomically in reestablish. Zero means no delivery has occurred yet.
	lastSeq uint64
}

// advanceLastSeq records seq as delivered if it moves the cursor forward,
// returning false when seq is not newer than the last delivered sequence — the
// caller must then drop the frame as overlap. The CAS-max loop keeps the cursor
// monotonic even when a replaced relay's in-flight pushes interleave with the
// new relay's replay around a reconnect.
func (s *subscription) advanceLastSeq(seq uint64) bool {
	for {
		last := atomic.LoadUint64(&s.lastSeq)
		if seq <= last {
			return false
		}
		if atomic.CompareAndSwapUint64(&s.lastSeq, last, seq) {
			return true
		}
	}
}

// reestablish replaces this subscription's relay generation after a NATS
// reconnect. The replaced relay can no longer be trusted: a surviving bus
// (plain blip, no restart) kept it pushing into the void while the client was
// disconnected, and once the connection is back it resumes pushing — frames
// from BEYOND that void, which must not advance the cursor. Two mechanisms
// make the replacement exact:
//
//   - The old generation's handler stopped accepting frames the moment the
//     connection reconnected (its reconnect count is stale — see relayHandler),
//     so lastSeq is frozen at the last frame the handler saw before the drop.
//   - The new generation gets a FRESH sub-id, hence a fresh private delivery
//     subject: anything the old relay still has in flight (it can publish a
//     final in-hand frame even after its stop is acknowledged) lands on a
//     subject this generation never subscribes — frames are attributable to
//     exactly one relay, with no timing assumptions.
//
// The pass unsubscribes the old delivery subject, stops the old relay on the
// bus (idempotent — a no-op after a real restart), subscribes the new delivery
// subject, and sends a fresh message.subscribe carrying the resume sequence
// (last delivered+1), which the bus maps to StartFromSeq on the backend. A
// zero lastSeq means no message was ever delivered; in that case it re-uses
// the original start option (DeliverAll or StartNew).
//
// Concurrency note: called from reestablishSubs (NATS reconnect goroutine).
// Reads lastSeq atomically and swaps the generation pair under s.mu; the other
// fields are written once at construction and are read-only here.
func (s *subscription) reestablish(c *Client) error {
	s.mu.Lock()
	oldSubID, oldNatsSub := s.subID, s.natsSub
	s.mu.Unlock()

	// The old generation's handler already drops everything, so unsubscribing
	// here only sheds queued frames early. An already-invalid subscription
	// (a previous pass failed after this point) is fine to unsubscribe again.
	if oldNatsSub != nil {
		_ = oldNatsSub.Unsubscribe()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.call(ctx, wireapi.OpSubscriptionStop, wireapi.SubscriptionStopInput{SubID: oldSubID}, nil); err != nil {
		return fmt.Errorf("stop stale relay: %w", err)
	}

	subID := ulid.Make().String()
	natsSub, err := c.nc.Subscribe(wireapi.DeliverSubject(c.id, subID), c.relayHandler(s, c.nc.Stats().Reconnects))
	if err != nil {
		return fmt.Errorf("subscribe delivery: %w", err)
	}
	// Swap the generation in BEFORE the bus call: whatever happens to the call,
	// teardown and the next resume pass must target the newest sub-id — a relay
	// whose subscribe landed but whose reply was lost is then found and stopped
	// by the next pass (or by teardown) rather than orphaned.
	s.mu.Lock()
	s.subID, s.natsSub = subID, natsSub
	s.mu.Unlock()

	// Snapshot the resume point after the old generation is fully out of the
	// picture: its handler froze lastSeq at the reconnect and its relay is now
	// stopped, so last is the exact high-water mark of what the handler saw.
	last := atomic.LoadUint64(&s.lastSeq)
	in := wireapi.SubscribeInput{
		Subject:    s.subject,
		SubID:      subID,
		DeliverAll: s.deliverAll,
	}
	if last > 0 {
		in.SinceSeq = last + 1
		in.DeliverAll = false // SinceSeq takes priority; don't replay from the top
	}
	return c.call(ctx, wireapi.OpMessageSubscribe, in, nil)
}

// Stop ends the subscription (safe to call more than once). It marks the
// subscription stopped first — synchronously, so a concurrent reconnect skips
// it — then cancels the internal context, which the bridge goroutine observes
// to run teardown.
func (s *subscription) Stop() {
	s.stopped.Store(true)
	s.cancel()
}

// teardown unsubscribes the delivery subject and asks the bus to stop the relay
// (the newest generation's — a concurrent resume pass that rotates afterwards is
// covered by reestablishSubs's post-pass stopped check). It runs exactly once,
// whether reached via Stop or a cancelled ctx (it also sets stopped, covering
// the ctx-cancel path that bypasses Stop).
func (s *subscription) teardown() {
	s.once.Do(func() {
		s.stopped.Store(true)
		s.mu.Lock()
		subID, natsSub := s.subID, s.natsSub
		s.mu.Unlock()
		if natsSub != nil {
			_ = natsSub.Unsubscribe()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.c.call(ctx, wireapi.OpSubscriptionStop, wireapi.SubscriptionStopInput{SubID: subID}, nil)
	})
}

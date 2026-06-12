package sextant

import (
	"context"
	"encoding/json"
	"errors"
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

// ErrResumeDeferred marks a non-fatal OnError notice: a reconnect-time resume
// attempt failed on transport (the bus never answered — a timeout, or a second
// blip inside the resume window), so the subscription stays registered and the
// next reconnect retries the resume. Until a retry succeeds the subscription
// delivers nothing; the notice makes that window visible. Distinguish it with
// errors.Is(err, ErrResumeDeferred) — any OnError that does not wrap it is
// fatal, and the subscription is stopped.
var ErrResumeDeferred = errors.New("sextant: subscription resume deferred to the next reconnect")

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

// OnError registers a handler that is called when a reconnect-time resume
// fails. A subscription that was live when the connection dropped will attempt
// to resume on reconnect; the handler then sees one of two distinguishable
// errors:
//
//   - Fatal: the bus answered that the resume is impossible (e.g. the store was
//     wiped and the sequence is gone). The subscription is stopped.
//   - Non-fatal: the resume failed on transport — the bus never answered — and
//     wraps ErrResumeDeferred (check errors.Is). The subscription stays
//     registered and the next reconnect retries it; until then it delivers
//     nothing, which the notice makes visible.
//
// Without OnError, either case is only logged — the handler receives nothing,
// forever. Registering an OnError makes that silence visible.
//
// The handler runs on the SDK's resume-pass goroutine. It should not block —
// a blocking handler stalls the remaining subscriptions' resumes behind it —
// and it should not make calls on this client (mid-reconnect they time out,
// stalling the pass further). Hand the error off to a channel and return.
func OnError(h func(error)) SubOption {
	return func(c *subConfig) { c.onErr = h }
}

// Publish sends record to subject, which must be in the messages space (msg.*),
// as a message.publish call: the bus stamps the frame (id, author, epoch) and
// appends it to the durable log, replying once the log has it (ADR-0019). The
// client supplies only the subject and record.
func (c *Client) Publish(ctx context.Context, subject string, record json.RawMessage) error {
	_, err := c.PublishMsg(ctx, subject, record)
	return err
}

// PublishMsg is Publish with the bus-stamped frame id and sequence returned.
// Callers that need to suppress self-echo (e.g. the MCP server) use this to
// record the published id before it can arrive back on a subscription.
func (c *Client) PublishMsg(ctx context.Context, subject string, record json.RawMessage) (wireapi.PublishOutput, error) {
	if !strings.HasPrefix(subject, sx.MessagePrefix) {
		return wireapi.PublishOutput{}, fmt.Errorf("sextant: publish subject %q is not in the messages space (%s*)", subject, sx.MessagePrefix)
	}
	var out wireapi.PublishOutput
	if err := c.call(ctx, wireapi.OpMessagePublish, wireapi.PublishInput{Subject: subject, Record: record}, &out); err != nil {
		return wireapi.PublishOutput{}, err
	}
	return out, nil
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
	return c.subscribe(ctx, subject, h, cfg, c.nc.Stats().Reconnects)
}

// subscribe is Subscribe with the first relay generation's epoch injected.
// Production code always passes the live reconnect count; the seam lets the
// silent-death regression test plant a stale epoch, standing in for a reconnect
// that completes between Subscribe's capture and the registration below.
func (c *Client) subscribe(ctx context.Context, subject string, h Handler, cfg subConfig, epoch uint64) (Subscription, error) {
	subID := ulid.Make().String()
	deliver := wireapi.DeliverSubject(c.id, subID)

	s := &subscription{
		c:          c,
		subID:      subID,
		epoch:      epoch,
		subject:    subject,
		deliverAll: cfg.deliverAll,
		handler:    h,
		onErr:      cfg.onErr,
	}

	// Subscribe to the delivery subject BEFORE making the call, so a frame the bus
	// relays the instant it replies can't outrun our subscription.
	natsSub, err := c.nc.Subscribe(deliver, c.relayHandler(s, epoch))
	if err != nil {
		return nil, fmt.Errorf("sextant: subscribe delivery: %w", err)
	}
	s.natsSub = natsSub

	subCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Register BEFORE the subscribe call, so a reconnect firing inside the call
	// window sees this subscription — otherwise its relay is never re-established
	// and it stays silently relay-less. A reconnect pass that catches it mid-call
	// is safe: the pass rotates to a fresh sub-id, this generation's handler
	// drops anything a late-landing relay pushes (its reconnect count is stale),
	// and the failure path below tears down whatever generation is newest. The
	// one window no pass covers — a reconnect completing between the epoch
	// capture in Subscribe and this registration — is closed by the staleness
	// re-check at the end of this function.
	c.registerSub(s)

	if err := c.call(ctx, wireapi.OpMessageSubscribe, wireapi.SubscribeInput{
		Subject:    subject,
		SubID:      subID,
		DeliverAll: cfg.deliverAll,
	}, nil); err != nil {
		c.deregisterSub(s)
		cancel()
		// Full teardown, not just the local unsubscribe: a reconnect pass racing
		// this call may have established a relay for this subscription, and
		// teardown's bus-side subscription.stop (idempotent, deadline-bounded)
		// clears the newest generation's relay.
		s.teardown()
		return nil, err
	}

	// Bridge ctx cancellation to teardown: a cancelled ctx ends the subscription
	// (unsubscribe + bus-side relay stop), same as an explicit Stop. The bridge
	// also unblocks on Stop (which cancels subCtx), so it never leaks. Started
	// before the staleness re-check, so a caller-side cancel still winds the
	// subscription down promptly even while a re-check rotation is in flight.
	go func() {
		<-subCtx.Done()
		s.teardown()
		c.deregisterSub(s)
	}()

	// Staleness re-check: the generation's epoch was captured BEFORE registerSub,
	// so a reconnect that completed inside that gap ran its resume pass without
	// seeing this subscription — nothing would ever rotate it, and relayHandler
	// would drop every frame, forever, with no OnError and no log: the
	// permanently-silent state ADR-0027 forbids. If the counter moved, rotate
	// now. Reading s.epoch (not the local capture) skips the rotation when a
	// pass that did see the registration already rotated us; reestablish
	// serializes with a concurrent pass (resumeMu) either way.
	s.mu.Lock()
	genEpoch := s.epoch
	s.mu.Unlock()
	if c.nc.Stats().Reconnects != genEpoch && !s.stopped.Load() {
		if err := s.reestablish(c); err != nil {
			// Fail loud: at Subscribe time an error means no subscription — the
			// deferral tier exists for already-established ones. Clean up
			// synchronously (the bridge's duplicate teardown/deregister are no-ops).
			cancel()
			s.teardown()
			c.deregisterSub(s)
			return nil, fmt.Errorf("sextant: subscribe: re-establish after a mid-subscribe reconnect: %w", err)
		}
		if s.stopped.Load() {
			s.stopNewestRelay() // Stop raced the rotation; sweep the newest generation
		}
	}
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

// startResumePass hands one resume pass to its own goroutine and returns
// immediately. It runs in the ReconnectHandler, which shares the NATS client's
// async-callback dispatcher with every other notification: one rotation is
// deadline-bounded, but a pass is unbounded in aggregate (N subscriptions ×
// 10s against a sick bus), so running it inline would wedge every later
// disconnect/reconnect notice behind it. The snapshot and the pass token are
// taken synchronously here, so the pass works exactly the set that existed at
// its reconnect; a subscription created after that carries a fresh epoch and
// needs no pass (Subscribe's staleness re-check covers its own window).
//
// At most one pass runs per token, and the newest token wins. The token is
// the connection's reconnect count as the pass is spawned: when the counter
// moves on, a newer reconnect exists (its handler is guaranteed to start a
// fresh pass over a full snapshot), so the running pass's remaining rotations
// are stale and it stops at the next subscription boundary; only its single
// in-flight rotation may still finish. Sibling handlers can carry EQUAL
// tokens — nats.go bumps the counter under the connection lock and only then
// queues the handler on the serial async dispatcher, so two rapid reconnects
// can hand both handlers the same, latest count. The claim slot (passClaimed)
// admits exactly one pass per token; the sibling skips and does not log, so
// one recovery produces exactly one "reconnected to the bus" completion.
//
// Convergence when a superseded pass's in-flight rotation overlaps the newer
// pass (resumeMu serializes the two on the same subscription):
//
//   - It lands first with a pre-bump epoch: the generation it installed is
//     doomed (relayHandler drops on a stale count, lastSeq stays frozen) and
//     the newer pass rotates it again from the frozen cursor — the normal
//     reconnect story.
//   - It lands first with a post-bump epoch: the generation is already healthy;
//     the newer pass's re-rotation replaces it from last+1 — exact, and the
//     monotonic cursor drops any overlap.
//   - It lands second (after the newer pass already rotated): it replaces the
//     newer pass's generation the same way — fresh epoch, resume from last+1.
//   - It fails on transport: at worst a lost-reply relay sits under the newest
//     sub-id, which the next rotation's opening idempotent stop clears.
//
// The "reconnected to the bus" log keeps its meaning: it fires only at the end
// of a completed, non-superseded pass — once every relay this reconnect owed
// is live again (or has failed loudly) — so callers waiting on that log see a
// ready bus.
func (c *Client) startResumePass() {
	// Read the spawn inputs first — Stats blocks on the connection lock, which
	// Close also takes, so this can stall arbitrarily long relative to Close.
	token := c.nc.Stats().Reconnects
	active := c.snapshotSubs()

	if c.passSpawnHook != nil {
		c.passSpawnHook() // test seam: stall here, between the reads and the claim
	}

	// The closed re-check and the Add form one critical section with Close's
	// close(closed) (see passMu): once Close has signalled, no pass can be
	// added to the WaitGroup it is about to drain — sync.WaitGroup requires
	// every Add to happen before the Wait.
	c.passMu.Lock()
	select {
	case <-c.closed:
		c.passMu.Unlock()
		return // Close is winding the client down; nothing left to resume
	default:
	}
	if token+1 <= c.passClaimed {
		c.passMu.Unlock()
		return // an equal-or-newer pass is already claimed (sibling handler); it owns this recovery
	}
	c.passClaimed = token + 1
	c.passWG.Add(1)
	c.passMu.Unlock()

	go func() {
		defer c.passWG.Done()
		if c.reestablishSubs(token, active) {
			c.logf("sextant: reconnected to the bus")
		}
	}()
}

// snapshotSubs copies the active subscription set under the registry lock, so
// a pass can walk it without holding the lock across network calls.
func (c *Client) snapshotSubs() []*subscription {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	active := make([]*subscription, 0, len(c.subs))
	for s := range c.subs {
		active = append(active, s)
	}
	return active
}

// reestablishSubs is one resume pass: it walks the snapshot and re-creates
// each subscription's server-side relay, resuming from last-delivered+1 so no
// messages are missed or duplicated. A subscription that had no deliveries
// resumes from its original start option. It reports whether the pass
// completed as the final pass — false when it was superseded by a newer
// reconnect (the counter moved off token) or stopped by Close, in which case
// the "reconnected" completion belongs to its successor (or to no one).
//
// A failed re-establishment splits on who failed (ADR-0027 reserves loud death
// for an impossible resume, not a flaky network):
//
//   - The bus answered with an error (busError): the resume is impossible —
//     e.g. the sequence is gone after a wipe. OnError fires with a fatal error
//     and the subscription is stopped.
//   - Transport failure (the bus never answered — a timeout, or a second blip
//     inside the resume window): the outcome is unknown but recoverable. The
//     subscription stays registered and the next reconnect pass retries the
//     resume; no retry loop runs here — the NATS reconnect machinery provides
//     the cadence. OnError fires with a non-fatal ErrResumeDeferred notice so
//     the dead window is visible. A partially-applied attempt converges on
//     retry: the pass targets the newest sub-id, so a relay whose subscribe
//     landed but whose reply was lost is found and stopped by the next pass's
//     idempotent bus-side stop; its generation handler drops anything it
//     pushed in the meantime once a further reconnect intervenes, and the
//     deliver-side monotonic cursor drops any overlap regardless.
//
// Ownership: runs on a dedicated pass goroutine (startResumePass), never on
// the NATS async-callback dispatcher. A subscription stopped during (or just
// before) the loop is skipped via its stopped flag.
func (c *Client) reestablishSubs(token uint64, active []*subscription) bool {
	for _, s := range active {
		select {
		case <-c.closed:
			return false // Close is draining the client; stop, stay quiet
		default:
		}
		if c.nc.Stats().Reconnects != token {
			return false // superseded: a newer reconnect's pass owns a fresh snapshot
		}
		if s.stopped.Load() {
			continue // Stop won the race; nothing to re-establish
		}
		if err := s.reestablish(c); err != nil {
			if s.stopped.Load() {
				continue // stopped concurrently; the failure is teardown, not loss
			}
			var be *busError
			if !errors.As(err, &be) {
				// Transport failure: stay registered, retry on the next reconnect.
				c.logf("sextant: subscription on %q: resume deferred to the next reconnect: %v", s.subject, err)
				if s.onErr != nil {
					s.onErr(fmt.Errorf("%w: subscription on %q delivers nothing until then: %v", ErrResumeDeferred, s.subject, err))
				}
				continue
			}
			c.logf("sextant: subscription on %q cannot resume after reconnect: %v", s.subject, err)
			if s.onErr != nil {
				s.onErr(fmt.Errorf("sextant: subscription on %q lost after reconnect: %w", s.subject, err))
			}
			s.cancel() // tears down via the bridge goroutine
			continue
		}
		if s.stopped.Load() {
			// Stop ran while we were re-establishing: teardown swept the pair it
			// saw, which may be the pre-rotation one. Sweep the newest generation —
			// its bus-side relay AND the rotated-in NATS subscription, which would
			// otherwise live on the connection for the client's life.
			s.stopNewestRelay()
		}
	}
	// Completed every rotation — final only if no newer reconnect has taken
	// over in the meantime and the client is not closing.
	select {
	case <-c.closed:
		return false
	default:
	}
	return c.nc.Stats().Reconnects == token
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

	// resumeMu serializes rotations (reestablish): the reconnect pass and
	// Subscribe's post-call staleness re-check can both request one, and two
	// interleaved rotations could strand a relay on the bus. Held across the
	// rotation's network calls; never taken while holding mu.
	resumeMu sync.Mutex

	// mu guards the live relay generation: the (subID, natsSub) pair and the
	// epoch (the connection's reconnect count) it was established under. Every
	// resume pass rotates to a fresh sub-id (see reestablish), so the trio
	// changes over the subscription's life; teardown and the resume passes read
	// the newest values under the lock.
	mu      sync.Mutex
	subID   string
	natsSub *nats.Subscription
	epoch   uint64

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
// Concurrency note: called from a resume pass (its dedicated goroutine — and
// briefly from two when a superseded pass's in-flight rotation overlaps its
// successor) and from Subscribe's post-call staleness re-check (the caller's
// goroutine); resumeMu serializes all of them. Reads lastSeq atomically and
// swaps the generation under s.mu; the other fields are written once at
// construction and are read-only here.
func (s *subscription) reestablish(c *Client) error {
	s.resumeMu.Lock()
	defer s.resumeMu.Unlock()

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
	epoch := c.nc.Stats().Reconnects
	natsSub, err := c.nc.Subscribe(wireapi.DeliverSubject(c.id, subID), c.relayHandler(s, epoch))
	if err != nil {
		return fmt.Errorf("subscribe delivery: %w", err)
	}
	// Swap the generation in BEFORE the bus call: whatever happens to the call,
	// teardown and the next resume pass must target the newest sub-id — a relay
	// whose subscribe landed but whose reply was lost is then found and stopped
	// by the next pass (or by teardown) rather than orphaned.
	s.mu.Lock()
	s.subID, s.natsSub, s.epoch = subID, natsSub, epoch
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
	// Known window (tracked in the backlog; not redesigned here): with a zero
	// cursor on a live-only subscription, this re-subscribe falls back to the
	// original start option — "new messages only" — so a message published
	// between the stop above and this call landing is not delivered, and no
	// later replay recovers it. Every other shape is covered: lastSeq > 0
	// resumes via SinceSeq (the replay closes the window exactly) and
	// DeliverAll replays from the top. Only the never-delivered live-only
	// case leaks, and only when a publish races into that stop→subscribe gap.
	return c.call(ctx, wireapi.OpMessageSubscribe, in, nil)
}

// stopNewestRelay sweeps the newest relay generation — its NATS subscription
// and its bus-side relay — after a rotation raced teardown: teardown swept the
// pair it saw, but a rotation in flight may have swapped a fresh pair in after
// that sweep. Both halves are idempotent (unsubscribing an already-invalid
// NATS subscription errors harmlessly; the bus stop is a no-op for a sub-id it
// no longer tracks), so it is safe after any rotation.
func (s *subscription) stopNewestRelay() {
	s.mu.Lock()
	subID, natsSub := s.subID, s.natsSub
	s.mu.Unlock()
	if natsSub != nil {
		_ = natsSub.Unsubscribe()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.c.call(ctx, wireapi.OpSubscriptionStop, wireapi.SubscriptionStopInput{SubID: subID}, nil)
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

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
// re-established after a bus restart. A subscription that was live when the bus
// went down will attempt to resume on reconnect; if that fails (e.g. the store
// was wiped) the handler is called with the error and the subscription is
// stopped. Without OnError, a failed resume is only logged — the handler
// receives nothing, forever. Registering an OnError makes that silence visible.
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
// A Subscription survives a bus restart (ADR-0027): on reconnect the SDK
// re-establishes the server-side relay, resuming from the last delivered sequence
// so no messages are missed or duplicated. If re-establishment is impossible
// (e.g. the store was wiped), the OnError handler is called and the subscription
// is stopped — never silent.
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
	natsSub, err := c.nc.Subscribe(deliver, func(m *nats.Msg) {
		var d wireapi.MessageDelivery
		if err := json.Unmarshal(m.Data, &d); err != nil {
			c.logf("sextant: undecodable delivery on %s, skipping: %v", subject, err)
			return
		}
		c.deliver(d, h, s)
	})
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
		c.deregisterSub(s.subID)
	}()
	return s, nil
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
	// Track the last successfully delivered sequence for reconnect resume.
	if s != nil && d.Seq > 0 {
		atomic.StoreUint64(&s.lastSeq, d.Seq)
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
// only long enough to snapshot the active set; each subscription's own mu
// guards its fields during the re-establish call.
func (c *Client) reestablishSubs() {
	c.subsMu.Lock()
	// Snapshot active subs so we don't hold the lock while making network calls.
	active := make([]*subscription, 0, len(c.subs))
	for _, s := range c.subs {
		active = append(active, s)
	}
	c.subsMu.Unlock()

	for _, s := range active {
		if err := s.reestablish(c); err != nil {
			c.logf("sextant: subscription %s on %q cannot resume after reconnect: %v", s.subID, s.subject, err)
			if s.onErr != nil {
				s.onErr(fmt.Errorf("sextant: subscription on %q lost after bus restart: %w", s.subject, err))
			}
			s.cancel() // tears down via the bridge goroutine
		}
	}
}

// registerSub adds a subscription to the client's active set.
// Ownership: c.subsMu guards c.subs.
func (c *Client) registerSub(s *subscription) {
	c.subsMu.Lock()
	if c.subs == nil {
		c.subs = make(map[string]*subscription)
	}
	c.subs[s.subID] = s
	c.subsMu.Unlock()
}

// deregisterSub removes a subscription from the client's active set.
func (c *Client) deregisterSub(subID string) {
	c.subsMu.Lock()
	delete(c.subs, subID)
	c.subsMu.Unlock()
}

type subscription struct {
	c          *Client
	subID      string
	subject    string
	deliverAll bool
	handler    Handler
	natsSub    *nats.Subscription
	cancel     context.CancelFunc
	onErr      func(error)
	once       sync.Once

	// lastSeq is the stream sequence of the last message successfully passed to
	// the handler. It is updated atomically on every delivery and read (also
	// atomically) in reestablish. Zero means no delivery has occurred yet.
	lastSeq uint64
}

// reestablish re-creates the server-side relay after a NATS reconnect.
// It sends a fresh message.subscribe call carrying the resume sequence (last
// delivered+1), which the bus maps to StartFromSeq on the backend. A zero
// lastSeq means no message was delivered before the restart; in that case it
// re-uses the original start option (DeliverAll or StartNew).
//
// Concurrency note: called from reestablishSubs (NATS reconnect goroutine).
// Reads lastSeq atomically; all other fields are written once at construction
// and are read-only here.
func (s *subscription) reestablish(c *Client) error {
	last := atomic.LoadUint64(&s.lastSeq)
	in := wireapi.SubscribeInput{
		Subject:    s.subject,
		SubID:      s.subID,
		DeliverAll: s.deliverAll,
	}
	if last > 0 {
		in.SinceSeq = last + 1
		in.DeliverAll = false // SinceSeq takes priority; don't replay from the top
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.call(ctx, wireapi.OpMessageSubscribe, in, nil)
}

// Stop ends the subscription (safe to call more than once). It cancels the
// internal context, which the bridge goroutine observes to run teardown.
func (s *subscription) Stop() { s.cancel() }

// teardown unsubscribes the delivery subject and asks the bus to stop the relay.
// It runs exactly once, whether reached via Stop or a cancelled ctx.
func (s *subscription) teardown() {
	s.once.Do(func() {
		if s.natsSub != nil {
			_ = s.natsSub.Unsubscribe()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.c.call(ctx, wireapi.OpSubscriptionStop, wireapi.SubscriptionStopInput{SubID: s.subID}, nil)
	})
}

package sextant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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
}

// SubOption configures Subscribe.
type SubOption func(*subConfig)

// DeliverAll replays the full backlog on the subject before live messages.
// Without it, a subscription delivers only messages published after it starts.
func DeliverAll() SubOption {
	return func(c *subConfig) { c.deliverAll = true }
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
func (c *Client) Subscribe(ctx context.Context, subject string, h Handler, opts ...SubOption) (Subscription, error) {
	var cfg subConfig
	for _, o := range opts {
		o(&cfg)
	}
	subID := ulid.Make().String()
	deliver := wireapi.DeliverSubject(c.id, subID)
	// Subscribe to the delivery subject BEFORE making the call, so a frame the bus
	// relays the instant it replies can't outrun our subscription.
	natsSub, err := c.nc.Subscribe(deliver, func(m *nats.Msg) {
		var d wireapi.MessageDelivery
		if err := json.Unmarshal(m.Data, &d); err != nil {
			c.logf("sextant: undecodable delivery on %s, skipping: %v", subject, err)
			return
		}
		c.deliver(d, h)
	})
	if err != nil {
		return nil, fmt.Errorf("sextant: subscribe delivery: %w", err)
	}
	if err := c.call(ctx, wireapi.OpMessageSubscribe, wireapi.SubscribeInput{
		Subject:    subject,
		SubID:      subID,
		DeliverAll: cfg.deliverAll,
	}, nil); err != nil {
		_ = natsSub.Unsubscribe()
		return nil, err
	}
	subCtx, cancel := context.WithCancel(ctx)
	s := &subscription{c: c, subID: subID, natsSub: natsSub, cancel: cancel}
	// Bridge ctx cancellation to teardown: a cancelled ctx ends the subscription
	// (unsubscribe + bus-side relay stop), same as an explicit Stop. The bridge
	// also unblocks on Stop (which cancels subCtx), so it never leaks.
	go func() {
		<-subCtx.Done()
		s.teardown()
	}()
	return s, nil
}

// deliver applies the receiver-side quarantine to one pushed delivery and, if it
// passes, hands it to the handler. The check mirrors the pull path: a replayed
// frame (DeliverAll) can carry a prior epoch, and — until the per-client
// allow-list lands — a client can still raw-publish to msg.>, so the SDK never
// trusts that a delivered frame is well-formed.
func (c *Client) deliver(d wireapi.MessageDelivery, h Handler) {
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
	h(Message{
		Frame:    frame,
		Subject:  d.Subject,
		BusTime:  d.BusTime,
		Sequence: d.Seq,
	})
}

type subscription struct {
	c       *Client
	subID   string
	natsSub *nats.Subscription
	cancel  context.CancelFunc
	once    sync.Once
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

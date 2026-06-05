package sextant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
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
// e.g. sx.TopicSubject("plan") or "msg.>") to h. Replay is client-controlled
// (see DeliverAll), and the consumer is ephemeral, so the bus keeps no
// per-subscriber state. Each message is re-checked against the wire contract
// (structure, epoch) and the bus clock, and quarantined (skipped + logged) on a
// violation (ADR-0006, ADR-0010). The subscription runs until Stop is called or
// ctx is cancelled, whichever comes first.
func (c *Client) Subscribe(ctx context.Context, subject string, h Handler, opts ...SubOption) (Subscription, error) {
	var cfg subConfig
	for _, o := range opts {
		o(&cfg)
	}
	stream, err := c.js.Stream(ctx, sx.StreamMessages)
	if err != nil {
		return nil, fmt.Errorf("sextant: open messages stream: %w", err)
	}
	deliver := jetstream.DeliverNewPolicy
	if cfg.deliverAll {
		deliver = jetstream.DeliverAllPolicy
	}
	oc, err := stream.OrderedConsumer(ctx, jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{subject},
		DeliverPolicy:  deliver,
	})
	if err != nil {
		return nil, fmt.Errorf("sextant: consumer: %w", err)
	}
	subCtx, cancel := context.WithCancel(ctx)
	cc, err := oc.Consume(func(msg jetstream.Msg) { c.dispatch(msg, h) })
	if err != nil {
		cancel()
		return nil, fmt.Errorf("sextant: consume: %w", err)
	}
	// Bridge ctx cancellation to the consumer: the subscription tears down on
	// Stop (which cancels subCtx) or on the caller's ctx being cancelled,
	// whichever fires first — so a cancelled ctx can't leave it running.
	go func() {
		<-subCtx.Done()
		cc.Stop()
	}()
	return &subscription{cancel: cancel}, nil
}

func (c *Client) dispatch(msg jetstream.Msg, h Handler) {
	md, err := msg.Metadata()
	if err != nil {
		c.logf("sextant: message without metadata on %s, skipping: %v", msg.Subject(), err)
		return
	}
	frame, err := wire.Decode(msg.Data())
	if err != nil {
		c.logf("sextant: undecodable frame on %s, skipping: %v", msg.Subject(), err)
		return
	}
	// Receiver-side quarantine: a client can raw-publish to msg.> (the guardrail
	// only denies sx.control + stream lifecycle), so the SDK re-checks every
	// consumed message against the wire contract before delivering it, rather
	// than trusting that it came through Client.Publish.
	if err := frame.Validate(); err != nil {
		c.logf("sextant: quarantined malformed frame on %s: %v", msg.Subject(), err)
		return
	}
	// Epoch is checked per-message, not just at connect, because durable streams
	// outlive epochs (ADR-0010): a replayed message from a prior epoch must not
	// be delivered as if it were current.
	if err := wire.CheckEpoch(frame.Epoch, wire.Epoch); err != nil {
		c.logf("sextant: quarantined %s on %s: %v", frame.ID, msg.Subject(), err)
		return
	}
	// Skew check against the trusted bus timestamp (ADR-0006).
	if err := wire.CheckSkew(frame.ID, md.Timestamp, c.skewTol); err != nil {
		c.logf("sextant: quarantined %s on %s: %v", frame.ID, msg.Subject(), err)
		return
	}
	h(Message{
		Frame:    frame,
		Subject:  msg.Subject(),
		BusTime:  md.Timestamp,
		Sequence: md.Sequence.Stream,
	})
}

type subscription struct {
	cancel context.CancelFunc
}

// Stop ends the subscription. It cancels the internal context, which the bridge
// goroutine observes to stop the underlying consumer; safe to call more than once.
func (s *subscription) Stop() { s.cancel() }

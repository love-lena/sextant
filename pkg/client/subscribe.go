package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant-initial/pkg/sextantproto"
)

// Message wraps a received envelope with the JetStream metadata callers
// need to resume after a disconnect. Subject is the concrete subject the
// envelope landed on (the wildcard subscription's bound subject).
//
// Ack acks the message to JetStream. It is safe to call Ack at most
// once; subsequent calls return nil.
type Message struct {
	Envelope    sextantproto.Envelope
	Subject     string
	StreamSeq   uint64
	ConsumerSeq uint64
	Timestamp   time.Time
	Ack         func() error
}

// SubscribeOption configures a Subscribe call. Use the With* helpers.
type SubscribeOption interface {
	applySubscribe(*subscribeOptions)
}

type subscribeOptions struct {
	fromSeq    uint64
	deliverAll bool
}

type subscribeOptFunc func(*subscribeOptions)

func (f subscribeOptFunc) applySubscribe(o *subscribeOptions) { f(o) }

// WithStartSeq starts delivery at the given JetStream stream sequence.
// Equivalent to calling SubscribeFromSeq. Mutually exclusive with
// WithDeliverAll; the last applied option wins.
func WithStartSeq(seq uint64) SubscribeOption {
	return subscribeOptFunc(func(o *subscribeOptions) {
		o.fromSeq = seq
		o.deliverAll = false
	})
}

// WithDeliverAll replays every message currently in the stream(s)
// matching the subject before transitioning to live.
func WithDeliverAll() SubscribeOption {
	return subscribeOptFunc(func(o *subscribeOptions) {
		o.deliverAll = true
		o.fromSeq = 0
	})
}

// Subscribe attaches an ordered JetStream consumer to subject and
// returns a channel of received messages. The channel closes when ctx
// is canceled or the Client is closed.
//
// Default delivery is "new" — only messages published after the consumer
// is created are delivered. Use WithStartSeq or WithDeliverAll to
// replay history.
//
// subject accepts NATS wildcards (`*` matches one token, `>` matches one
// or more) per specs/protocols/bus-subjects.md.
func (c *Client) Subscribe(ctx context.Context, subject string, opts ...SubscribeOption) (<-chan Message, error) {
	if c.isClosed() {
		return nil, ErrClosed
	}
	if subject == "" {
		return nil, fmt.Errorf("client: Subscribe requires a non-empty subject")
	}

	var so subscribeOptions
	for _, o := range opts {
		o.applySubscribe(&so)
	}

	cfg := jetstream.OrderedConsumerConfig{
		FilterSubjects: []string{subject},
		DeliverPolicy:  jetstream.DeliverNewPolicy,
		ReplayPolicy:   jetstream.ReplayInstantPolicy,
	}
	switch {
	case so.fromSeq > 0:
		cfg.DeliverPolicy = jetstream.DeliverByStartSequencePolicy
		cfg.OptStartSeq = so.fromSeq
	case so.deliverAll:
		cfg.DeliverPolicy = jetstream.DeliverAllPolicy
	}

	stream, err := c.resolveStream(ctx, subject)
	if err != nil {
		return nil, err
	}

	consumer, err := c.js.OrderedConsumer(ctx, stream, cfg)
	if err != nil {
		return nil, fmt.Errorf("client: ordered consumer on %q: %w", subject, err)
	}

	out := make(chan Message, 64)
	consumeCtx, err := consumer.Consume(func(m jetstream.Msg) {
		msg, err := toMessage(m)
		if err != nil {
			// Spec requires type-checked payloads. M4's contract:
			// terminate redelivery on malformed envelopes so the
			// consumer doesn't spin on garbage. Callers needing
			// payload errors should use a raw NATS subscription.
			_ = m.Term()
			return
		}
		select {
		case out <- msg:
		case <-ctx.Done():
		}
	})
	if err != nil {
		return nil, fmt.Errorf("client: consume on %q: %w", subject, err)
	}

	go func() {
		<-ctx.Done()
		consumeCtx.Stop()
		// Wait for the Consume loop to fully drain before closing
		// out, otherwise a late-firing handler could write to a
		// closed channel.
		<-consumeCtx.Closed()
		close(out)
	}()

	return out, nil
}

// SubscribeFromSeq is sugar for Subscribe(ctx, subject, WithStartSeq(fromSeq)).
func (c *Client) SubscribeFromSeq(ctx context.Context, subject string, fromSeq uint64) (<-chan Message, error) {
	return c.Subscribe(ctx, subject, WithStartSeq(fromSeq))
}

// resolveStream picks the JetStream stream the requested subject belongs
// to. Uses StreamNameBySubject which the server resolves authoritatively.
func (c *Client) resolveStream(ctx context.Context, subject string) (string, error) {
	name, err := c.js.StreamNameBySubject(ctx, subject)
	if err != nil {
		return "", fmt.Errorf("client: resolve stream for subject %q: %w", subject, err)
	}
	return name, nil
}

// toMessage converts a jetstream.Msg to a typed Message. Returns an
// error if the payload isn't a valid envelope or the metadata lookup
// fails.
func toMessage(m jetstream.Msg) (Message, error) {
	md, err := m.Metadata()
	if err != nil {
		return Message{}, fmt.Errorf("metadata: %w", err)
	}
	var env sextantproto.Envelope
	if err := json.Unmarshal(m.Data(), &env); err != nil {
		return Message{}, fmt.Errorf("unmarshal envelope: %w", err)
	}
	if err := env.Validate(); err != nil {
		return Message{}, fmt.Errorf("invalid envelope: %w", err)
	}
	return Message{
		Envelope:    env,
		Subject:     m.Subject(),
		StreamSeq:   md.Sequence.Stream,
		ConsumerSeq: md.Sequence.Consumer,
		Timestamp:   md.Timestamp,
		Ack:         m.Ack,
	}, nil
}

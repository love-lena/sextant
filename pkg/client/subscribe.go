package client

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/love-lena/sextant/pkg/sextantproto"
)

// Message wraps a received envelope with the JetStream metadata callers
// need to resume after a disconnect. Subject is the concrete subject the
// envelope landed on (the wildcard subscription's bound subject).
//
// On a payload-decode or envelope-validation failure, Subscribe delivers
// a Message with Err set, Subject and StreamSeq populated from the raw
// JetStream message (so callers can correlate / resume), and Envelope
// left zero. Callers must check Err before reading Envelope:
//
//	for m := range ch {
//	    if m.Err != nil {
//	        log.Printf("bad envelope on %s seq=%d: %v", m.Subject, m.StreamSeq, m.Err)
//	        continue
//	    }
//	    handle(m.Envelope)
//	}
//
// JetStream redelivery is short-circuited server-side (the message is
// Term'd before Err is surfaced), so a single malformed envelope is
// reported exactly once.
//
// Ack acks the message to JetStream. Safe to call multiple times: the
// underlying ack fires exactly once, and subsequent calls return nil.
type Message struct {
	Envelope    sextantproto.Envelope
	Subject     string
	StreamSeq   uint64
	ConsumerSeq uint64
	Timestamp   time.Time
	Ack         func() error
	// Err is non-nil when the JetStream message could not be decoded
	// into a valid sextantproto.Envelope. When Err is set, Envelope is
	// the zero value and Ack is a no-op.
	Err error
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

	// subCtx fans the cancellation tree out so either a caller-driven
	// ctx cancel OR a Client.Close hits the same path.
	subCtx, cancelSub := context.WithCancel(ctx)
	out := make(chan Message, 64)
	consumeCtx, err := consumer.Consume(func(m jetstream.Msg) {
		msg, decodeErr := toMessage(m)
		if decodeErr != nil {
			// Per specs/components/client-libraries.md §"Shared
			// concerns" the spec requires we surface type-validation
			// failures as errors to the caller, not silently coerce
			// or drop. Term the underlying message first so
			// JetStream doesn't redeliver garbage, then emit a
			// Message with Err set so the caller learns about it.
			subj, seq := messageCoordinates(m)
			_ = m.Term()
			select {
			case out <- Message{Subject: subj, StreamSeq: seq, Err: decodeErr, Ack: noopAck}:
			case <-subCtx.Done():
			}
			return
		}
		select {
		case out <- msg:
		case <-subCtx.Done():
		}
	})
	if err != nil {
		cancelSub()
		return nil, fmt.Errorf("client: consume on %q: %w", subject, err)
	}

	// Register a stopper on the Client so Close() can tear this
	// Subscribe down even when the caller passed a long-lived ctx.
	reg := c.register(func() { cancelSub() })

	go func() {
		<-subCtx.Done()
		consumeCtx.Stop()
		// Wait for the Consume loop to fully drain before closing
		// out, otherwise a late-firing handler could write to a
		// closed channel.
		<-consumeCtx.Closed()
		close(out)
		c.deregister(reg)
	}()

	return out, nil
}

// noopAck is the Ack closure attached to error Messages — there is no
// underlying delivery to ack (Term has already fired server-side).
func noopAck() error { return nil }

// messageCoordinates pulls the subject + stream seq off a JetStream
// message, returning zero-valued fallbacks if Metadata is unavailable.
// Used to populate the resume cursor on error Messages without erroring
// the whole stream when Metadata itself is the failure.
func messageCoordinates(m jetstream.Msg) (string, uint64) {
	subject := m.Subject()
	md, err := m.Metadata()
	if err != nil || md == nil {
		return subject, 0
	}
	return subject, md.Sequence.Stream
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
		Ack:         onceAck(m),
	}, nil
}

// onceAck wraps m.Ack so it fires exactly once. The first call returns
// whatever JetStream returns; subsequent calls return nil. Lets callers
// stash and replay Messages without having to track ack-state externally.
func onceAck(m jetstream.Msg) func() error {
	var once sync.Once
	var ackErr error
	return func() error {
		once.Do(func() { ackErr = m.Ack() })
		return ackErr
	}
}

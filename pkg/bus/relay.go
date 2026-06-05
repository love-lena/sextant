package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/love-lena/sextant/internal/backend"
	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
)

// Push-stream operations (ADR-0019): message.subscribe and artifact.watch start
// a server-side relay that bridges a backend stream into the client's private
// delivery space, sx.deliver.<clientID>.<subID>. The subscribe/watch call returns
// immediately (with the delivery subject); deliveries arrive asynchronously until
// the client ends the subscription with subscription.stop, or the bus shuts down.
//
// The relay runs on the bus's operator connection (full access), so the client
// reaches its stream only by owner-subscribing sx.deliver.<its-own-id>.>. The
// author on every relayed frame is already the bus-stamped one (the frame was
// stamped at publish/write time), so the push path inherits the same trust as the
// pull path.

// relay is one running subscription: a cancel that tears down its backend stream
// (its goroutine then drains and exits).
type relay struct {
	cancel context.CancelFunc
}

// registerRelay reserves (clientID, subID) and returns a context rooted at the
// bus relay context, so the relay dies on either an explicit stop or bus
// shutdown. It rejects a duplicate subID (the SDK generates a fresh ULID per
// subscription, so a collision is a client bug, not a race).
func (b *Bus) registerRelay(clientID, subID string) (context.Context, error) {
	b.relaysMu.Lock()
	defer b.relaysMu.Unlock()
	if b.relays[clientID][subID] != nil {
		return nil, fmt.Errorf("subscription %q already exists", subID)
	}
	ctx, cancel := context.WithCancel(b.relayCtx)
	subs := b.relays[clientID]
	if subs == nil {
		subs = make(map[string]*relay)
		b.relays[clientID] = subs
	}
	subs[subID] = &relay{cancel: cancel}
	return ctx, nil
}

// stopRelay cancels and removes one subscription. It is idempotent: a subID the
// bus no longer tracks is a no-op, so an explicit stop and the relay goroutine's
// own deferred cleanup can both run safely.
func (b *Bus) stopRelay(clientID, subID string) {
	b.relaysMu.Lock()
	var r *relay
	if subs := b.relays[clientID]; subs != nil {
		if r = subs[subID]; r != nil {
			delete(subs, subID)
			if len(subs) == 0 {
				delete(b.relays, clientID)
			}
		}
	}
	b.relaysMu.Unlock()
	if r != nil {
		r.cancel()
	}
}

// validSubID rejects a subscription id that isn't safe as a NATS subject token
// (it becomes the last token of sx.deliver.<id>.<subID>). A ULID — what the SDK
// sends — always passes.
func validSubID(subID string) error {
	if subID == "" {
		return errors.New("subscription id is empty")
	}
	if len(subID) > 64 {
		return fmt.Errorf("subscription id %q is too long (max 64)", subID)
	}
	if strings.ContainsAny(subID, ". *>\t\r\n") {
		return fmt.Errorf("subscription id %q has an illegal character", subID)
	}
	return nil
}

// opSubscribe starts a message.subscribe relay: it streams frames matching the
// subject (from the start the client chose) into the client's delivery subject.
func (b *Bus) opSubscribe(clientID string, data []byte) (json.RawMessage, error) {
	var in wireapi.SubscribeInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: subscribe: bad input: %w", err)
	}
	if !strings.HasPrefix(in.Subject, sx.MessagePrefix) {
		return nil, fmt.Errorf("bus: subscribe subject %q is not in the messages space (%s*)", in.Subject, sx.MessagePrefix)
	}
	if err := validSubID(in.SubID); err != nil {
		return nil, fmt.Errorf("bus: subscribe: %w", err)
	}
	start := backend.StartNew
	if in.DeliverAll {
		start = backend.StartAll
	}
	relayCtx, err := b.registerRelay(clientID, in.SubID)
	if err != nil {
		return nil, fmt.Errorf("bus: subscribe: %w", err)
	}
	ch, err := b.backend.Subscribe(relayCtx, in.Subject, start)
	if err != nil {
		b.stopRelay(clientID, in.SubID)
		return nil, fmt.Errorf("bus: subscribe: %w", err)
	}
	deliver := wireapi.DeliverSubject(clientID, in.SubID)
	go b.runMessageRelay(clientID, in.SubID, deliver, ch)
	return json.Marshal(wireapi.SubscribeOutput{DeliverSubject: deliver})
}

// runMessageRelay forwards each log entry to the delivery subject until the
// stream closes (stop or shutdown). It owns no state beyond the channel; the
// deferred stopRelay keeps the registry clean if the stream ends on its own.
func (b *Bus) runMessageRelay(clientID, subID, deliver string, ch <-chan backend.LogEntry) {
	defer b.stopRelay(clientID, subID)
	for e := range ch {
		frame, err := wire.Decode(e.Data)
		if err != nil {
			continue // skip an undecodable entry rather than break the stream
		}
		payload, err := json.Marshal(wireapi.MessageDelivery{
			SubID:   subID,
			Subject: e.Subject,
			Seq:     e.Seq,
			BusTime: e.Time,
			Frame:   frame,
		})
		if err != nil {
			continue
		}
		if err := b.opConn.Publish(deliver, payload); err != nil {
			return // operator connection gone (shutdown); stop relaying
		}
	}
}

// opArtifactWatch starts an artifact.watch relay: current value first, then each
// later write and delete, into the client's delivery subject.
func (b *Bus) opArtifactWatch(clientID string, data []byte) (json.RawMessage, error) {
	var in wireapi.WatchInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: artifact.watch: bad input: %w", err)
	}
	if in.Name == "" {
		return nil, errors.New("bus: artifact.watch: name is required")
	}
	if err := validSubID(in.SubID); err != nil {
		return nil, fmt.Errorf("bus: artifact.watch: %w", err)
	}
	relayCtx, err := b.registerRelay(clientID, in.SubID)
	if err != nil {
		return nil, fmt.Errorf("bus: artifact.watch: %w", err)
	}
	ch, err := b.backend.Watch(relayCtx, sx.BucketArtifacts, in.Name)
	if err != nil {
		b.stopRelay(clientID, in.SubID)
		return nil, fmt.Errorf("bus: artifact.watch: %w", err)
	}
	deliver := wireapi.DeliverSubject(clientID, in.SubID)
	go b.runArtifactRelay(clientID, in.SubID, in.Name, deliver, ch)
	return json.Marshal(wireapi.WatchOutput{DeliverSubject: deliver})
}

// runArtifactRelay forwards each artifact change to the delivery subject. A write
// carries the decoded record + bus-stamped timestamps; a delete carries neither.
func (b *Bus) runArtifactRelay(clientID, subID, name, deliver string, ch <-chan backend.Change) {
	defer b.stopRelay(clientID, subID)
	for c := range ch {
		d := wireapi.ArtifactDelivery{SubID: subID, Name: name, Revision: c.Revision, Deleted: c.Deleted}
		if !c.Deleted {
			frame, err := wire.Decode(c.Value)
			if err != nil {
				continue // skip an undecodable record rather than break the stream
			}
			d.Record = frame.Record
			d.CreatedAt = frame.CreatedAt
			d.UpdatedAt = frame.UpdatedAt
		}
		payload, err := json.Marshal(d)
		if err != nil {
			continue
		}
		if err := b.opConn.Publish(deliver, payload); err != nil {
			return
		}
	}
}

// opSubscriptionStop ends a subscription the caller owns. It is keyed under the
// caller's own clientID, so a client can only stop its own subscriptions, and a
// SubID the bus no longer tracks is a success (idempotent teardown).
func (b *Bus) opSubscriptionStop(clientID string, data []byte) (json.RawMessage, error) {
	var in wireapi.SubscriptionStopInput
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("bus: subscription.stop: bad input: %w", err)
	}
	b.stopRelay(clientID, in.SubID)
	return json.Marshal(struct{}{})
}

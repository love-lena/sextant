package sextant

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/nats-io/nats.go"
	"github.com/oklog/ulid/v2"
)

// The principal designation (ADR-0030): the bus records one principal — a human's
// client ULID — in a client-readable, Operator-writable sx key. A client
// discovers it on connect (folded into the hello handshake; see Principal) and
// keeps it current with WatchPrincipal, so it observes a re-designation without
// reconnecting. A client never reads the sx_meta KV directly — the per-client
// allow-list forbids it — so both the one-shot read (GetPrincipal) and the watch
// go through bus operations, the same as every other client call.
//
// Setting the principal is operator-only and lives on the Issuer (the operator
// credential), not here: a regular client can read the designation but never
// write it (the bus rejects principal.set from a non-operator caller).

// GetPrincipal reads the current principal ULID as a one-shot principal.get call.
// Empty means no principal is designated. A connected client usually reads the
// cached value with Principal() instead; GetPrincipal is the explicit re-read
// (e.g. a CLI `principal get`).
func (c *Client) GetPrincipal(ctx context.Context) (string, error) {
	var out wireapi.PrincipalGetOutput
	if err := c.call(ctx, wireapi.OpPrincipalGet, wireapi.PrincipalGetInput{}, &out); err != nil {
		return "", err
	}
	// Keep the cached value in step with an explicit read.
	c.setPrincipal(out.Principal)
	return out.Principal, nil
}

// WatchPrincipal calls h on each principal designation as a principal.watch call:
// the bus relays the current value first, then each re-designation, to this
// client's private delivery subject, and the SDK fans them out to h. Every
// delivery also advances the client's cached value, so Principal() stays current
// for the life of the watch. The watch runs until Stop is called or ctx is
// cancelled, whichever comes first.
func (c *Client) WatchPrincipal(ctx context.Context, h func(principal string)) (Watch, error) {
	subID := ulid.Make().String()
	deliver := wireapi.DeliverSubject(c.id, subID)
	// Subscribe before the call so a change the bus relays the instant it replies
	// can't outrun our subscription.
	natsSub, err := c.nc.Subscribe(deliver, func(m *nats.Msg) {
		var d wireapi.PrincipalDelivery
		if err := json.Unmarshal(m.Data, &d); err != nil {
			c.logf("sextant: undecodable principal delivery, skipping: %v", err)
			return
		}
		c.setPrincipal(d.Principal)
		if h != nil {
			h(d.Principal)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("sextant: principal watch delivery: %w", err)
	}
	if err := c.call(ctx, wireapi.OpPrincipalWatch, wireapi.PrincipalWatchInput{SubID: subID}, nil); err != nil {
		_ = natsSub.Unsubscribe()
		return nil, err
	}
	subCtx, cancel := context.WithCancel(ctx)
	w := &principalWatch{c: c, subID: subID, natsSub: natsSub, cancel: cancel}
	go func() {
		<-subCtx.Done()
		w.teardown()
	}()
	return w, nil
}

// principalWatch tears the watch down on Stop or ctx cancellation.
type principalWatch struct {
	c       *Client
	subID   string
	natsSub *nats.Subscription
	cancel  context.CancelFunc
	once    sync.Once
}

// Stop ends the watch (idempotent). It cancels the internal context, which the
// bridge goroutine observes to run teardown.
func (p *principalWatch) Stop() error {
	p.cancel()
	return nil
}

// teardown unsubscribes the delivery subject and asks the bus to stop the relay.
// It runs exactly once, whether reached via Stop or a cancelled ctx.
func (p *principalWatch) teardown() {
	p.once.Do(func() {
		if p.natsSub != nil {
			_ = p.natsSub.Unsubscribe()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.c.call(ctx, wireapi.OpSubscriptionStop, wireapi.SubscriptionStopInput{SubID: p.subID}, nil)
	})
}

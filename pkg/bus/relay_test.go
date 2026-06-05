package bus

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/nats-io/nats.go"
)

// relayCount reports how many relays the bus tracks for clientID (white-box, for
// leak assertions).
func (b *Bus) relayCount(clientID string) int {
	b.relaysMu.Lock()
	defer b.relaysMu.Unlock()
	return len(b.relays[clientID])
}

// TestSubscribeRelayLifecycle drives the push path through the wire API directly
// and asserts the bus-side relay registry: a subscribe registers exactly one
// relay, a duplicate sub_id is rejected, a publish is relayed to the delivery
// subject, and subscription.stop removes the relay (no leak). The SDK tests cover
// the client-facing behavior; this pins the server-side lifecycle that a
// best-effort Stop would otherwise let leak silently.
func TestSubscribeRelayLifecycle(t *testing.T) {
	b := startTestBus(t)
	nc := connectClient(t, b, "sub-life")
	const id, subID = "sub-life", "01TESTSUB0000000000000001"
	subj := sx.TopicSubject("life")

	// Owner-subscribe the delivery subject before starting the relay.
	got := make(chan *nats.Msg, 4)
	if _, err := nc.Subscribe(wireapi.DeliverSubject(id, subID), func(m *nats.Msg) { got <- m }); err != nil {
		t.Fatalf("subscribe delivery: %v", err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	if resp := call(t, nc, id, wireapi.OpMessageSubscribe, wireapi.SubscribeInput{Subject: subj, SubID: subID}); resp.Error != "" {
		t.Fatalf("subscribe: %s", resp.Error)
	}
	if n := b.relayCount(id); n != 1 {
		t.Fatalf("relay count after subscribe = %d, want 1", n)
	}

	// A duplicate sub_id is rejected (and starts no second relay).
	if dup := call(t, nc, id, wireapi.OpMessageSubscribe, wireapi.SubscribeInput{Subject: subj, SubID: subID}); dup.Error == "" {
		t.Error("duplicate sub_id should be rejected")
	}
	if n := b.relayCount(id); n != 1 {
		t.Fatalf("relay count after duplicate = %d, want 1", n)
	}

	// A publish is relayed to the delivery subject, stamped with the author.
	if p := call(t, nc, id, wireapi.OpMessagePublish, wireapi.PublishInput{Subject: subj, Record: json.RawMessage(`{"live":true}`)}); p.Error != "" {
		t.Fatalf("publish: %s", p.Error)
	}
	select {
	case m := <-got:
		var d wireapi.MessageDelivery
		if err := json.Unmarshal(m.Data, &d); err != nil {
			t.Fatalf("decode delivery: %v", err)
		}
		if d.SubID != subID {
			t.Errorf("delivery sub_id = %q, want %q", d.SubID, subID)
		}
		if d.Frame.Author != id {
			t.Errorf("delivery author = %q, want %q", d.Frame.Author, id)
		}
		if string(d.Frame.Record) != `{"live":true}` {
			t.Errorf("delivery record = %s", d.Frame.Record)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery on the subscription")
	}

	// subscription.stop removes the relay.
	if s := call(t, nc, id, wireapi.OpSubscriptionStop, wireapi.SubscriptionStopInput{SubID: subID}); s.Error != "" {
		t.Fatalf("stop: %s", s.Error)
	}
	if n := b.relayCount(id); n != 0 {
		t.Fatalf("relay count after stop = %d, want 0 (relay leaked)", n)
	}
}

package sextant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/love-lena/sextant/internal/wireapi"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/nats.go"
	"github.com/oklog/ulid/v2"
)

// Artifact is a named, versioned unit of durable shared work. Its Record is a
// Lexicon (typed JSON) — the same content model as a message's Record
// (ADR-0005, ADR-0016), so the two primitives share one record shape: a message
// is a lexicon in flight, an artifact a lexicon at rest. Updates are
// compare-and-set on Revision, which gives the single-author-at-a-time
// discipline: a writer must hold the current revision to change it.
type Artifact struct {
	Name     string
	Record   wire.Lexicon
	Revision uint64
	Created  time.Time
}

// validArtifactRecord rejects a record that isn't a non-empty JSON lexicon, so a
// malformed artifact never reaches the bus (fail-loud at the writer; the bus
// re-checks too).
func validArtifactRecord(r wire.Lexicon) error {
	if len(r) == 0 || !json.Valid(r) {
		return errors.New("sextant: artifact record must be a non-empty JSON lexicon")
	}
	return nil
}

// Watch is an active artifact watch; call Stop to end it.
type Watch interface {
	Stop() error
}

// CreateArtifact creates a new artifact from a Lexicon record as an
// artifact.create call: the bus stamps the frame (id, author, timestamps) and
// stores it. It fails if name already exists or record is not a valid lexicon.
func (c *Client) CreateArtifact(ctx context.Context, name string, record wire.Lexicon) (uint64, error) {
	if err := validArtifactRecord(record); err != nil {
		return 0, err
	}
	var out wireapi.ArtifactWriteOutput
	if err := c.call(ctx, wireapi.OpArtifactCreate, wireapi.ArtifactCreateInput{Name: name, Record: json.RawMessage(record)}, &out); err != nil {
		return 0, err
	}
	return out.Revision, nil
}

// UpdateArtifact compare-and-set updates an artifact as an artifact.update call:
// it succeeds only if the current revision equals expectedRev, otherwise it
// returns an error (a concurrent write moved it on). This is the single-author
// discipline; the bus enforces the compare-and-set.
func (c *Client) UpdateArtifact(ctx context.Context, name string, record wire.Lexicon, expectedRev uint64) (uint64, error) {
	if err := validArtifactRecord(record); err != nil {
		return 0, err
	}
	var out wireapi.ArtifactWriteOutput
	if err := c.call(ctx, wireapi.OpArtifactUpdate, wireapi.ArtifactUpdateInput{Name: name, Record: json.RawMessage(record), ExpectedRev: expectedRev}, &out); err != nil {
		return 0, err
	}
	return out.Revision, nil
}

// GetArtifact reads an artifact's current value and bus-stamped metadata as an
// artifact.get call.
func (c *Client) GetArtifact(ctx context.Context, name string) (Artifact, error) {
	var out wireapi.ArtifactGetOutput
	if err := c.call(ctx, wireapi.OpArtifactGet, wireapi.ArtifactGetInput{Name: name}, &out); err != nil {
		return Artifact{}, err
	}
	return Artifact{
		Name:     out.Name,
		Record:   wire.Lexicon(out.Record),
		Revision: out.Revision,
		Created:  parseArtifactTime(out.CreatedAt),
	}, nil
}

// DeleteArtifact removes an artifact as an artifact.delete call.
func (c *Client) DeleteArtifact(ctx context.Context, name string) error {
	return c.call(ctx, wireapi.OpArtifactDelete, wireapi.ArtifactDeleteInput{Name: name}, nil)
}

// ArtifactChange is a change delivered to a WatchArtifact handler: the artifact
// at this revision, plus whether the change was a deletion. On a delete the
// Record is empty and Deleted is true — so a watcher can tell a removal from a
// write rather than inferring it from an empty record.
type ArtifactChange struct {
	Artifact
	Deleted bool
}

// WatchArtifact calls h on each change to name as an artifact.watch call: the bus
// relays changes to this client's private delivery subject, starting with the
// current value if present, and the SDK fans them out to h. Deletes are delivered
// too (with Deleted set). The watch runs until Stop is called or ctx is
// cancelled, whichever comes first.
func (c *Client) WatchArtifact(ctx context.Context, name string, h func(ArtifactChange)) (Watch, error) {
	subID := ulid.Make().String()
	deliver := wireapi.DeliverSubject(c.id, subID)
	// Subscribe before the call so a change the bus relays the instant it replies
	// can't outrun our subscription.
	natsSub, err := c.nc.Subscribe(deliver, func(m *nats.Msg) {
		var d wireapi.ArtifactDelivery
		if err := json.Unmarshal(m.Data, &d); err != nil {
			c.logf("sextant: undecodable artifact delivery for %s, skipping: %v", name, err)
			return
		}
		ch := ArtifactChange{
			Artifact: Artifact{Name: d.Name, Revision: d.Revision},
			Deleted:  d.Deleted,
		}
		if !d.Deleted {
			ch.Record = wire.Lexicon(d.Record)
			ch.Created = parseArtifactTime(d.CreatedAt)
		}
		h(ch)
	})
	if err != nil {
		return nil, fmt.Errorf("sextant: watch delivery: %w", err)
	}
	if err := c.call(ctx, wireapi.OpArtifactWatch, wireapi.WatchInput{Name: name, SubID: subID}, nil); err != nil {
		_ = natsSub.Unsubscribe()
		return nil, err
	}
	subCtx, cancel := context.WithCancel(ctx)
	w := &artifactWatch{c: c, subID: subID, natsSub: natsSub, cancel: cancel}
	go func() {
		<-subCtx.Done()
		w.teardown()
	}()
	return w, nil
}

// artifactWatch tears the watch down on Stop or ctx cancellation.
type artifactWatch struct {
	c       *Client
	subID   string
	natsSub *nats.Subscription
	cancel  context.CancelFunc
	once    sync.Once
}

// Stop ends the watch (idempotent). It cancels the internal context, which the
// bridge goroutine observes to run teardown.
func (a *artifactWatch) Stop() error {
	a.cancel()
	return nil
}

// teardown unsubscribes the delivery subject and asks the bus to stop the relay.
// It runs exactly once, whether reached via Stop or a cancelled ctx.
func (a *artifactWatch) teardown() {
	a.once.Do(func() {
		if a.natsSub != nil {
			_ = a.natsSub.Unsubscribe()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.c.call(ctx, wireapi.OpSubscriptionStop, wireapi.SubscriptionStopInput{SubID: a.subID}, nil)
	})
}

// parseArtifactTime parses a bus RFC3339 timestamp, returning the zero time if it
// is empty or unparseable (a missing creation time is not worth failing a read).
func parseArtifactTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

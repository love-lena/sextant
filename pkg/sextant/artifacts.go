package sextant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/wire"
	"github.com/nats-io/nats.go/jetstream"
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
// malformed artifact never reaches the bucket (fail-loud at the writer).
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

func (c *Client) artifacts(ctx context.Context) (jetstream.KeyValue, error) {
	kv, err := c.js.KeyValue(ctx, sx.BucketArtifacts)
	if err != nil {
		return nil, fmt.Errorf("sextant: open artifacts: %w", err)
	}
	return kv, nil
}

// CreateArtifact creates a new artifact from a Lexicon record. It fails if name
// already exists or record is not a valid lexicon.
func (c *Client) CreateArtifact(ctx context.Context, name string, record wire.Lexicon) (uint64, error) {
	if err := validArtifactRecord(record); err != nil {
		return 0, err
	}
	kv, err := c.artifacts(ctx)
	if err != nil {
		return 0, err
	}
	rev, err := kv.Create(ctx, name, record)
	if err != nil {
		return 0, fmt.Errorf("sextant: create artifact %q: %w", name, err)
	}
	return rev, nil
}

// UpdateArtifact compare-and-set updates an artifact: it succeeds only if the
// current revision equals expectedRev, otherwise it returns an error (a
// concurrent write moved it on). This is the single-author discipline.
func (c *Client) UpdateArtifact(ctx context.Context, name string, record wire.Lexicon, expectedRev uint64) (uint64, error) {
	if err := validArtifactRecord(record); err != nil {
		return 0, err
	}
	kv, err := c.artifacts(ctx)
	if err != nil {
		return 0, err
	}
	rev, err := kv.Update(ctx, name, record, expectedRev)
	if err != nil {
		return 0, fmt.Errorf("sextant: update artifact %q (rev %d): %w", name, expectedRev, err)
	}
	return rev, nil
}

// GetArtifact reads an artifact's current value and revision.
func (c *Client) GetArtifact(ctx context.Context, name string) (Artifact, error) {
	kv, err := c.artifacts(ctx)
	if err != nil {
		return Artifact{}, err
	}
	e, err := kv.Get(ctx, name)
	if err != nil {
		return Artifact{}, fmt.Errorf("sextant: get artifact %q: %w", name, err)
	}
	return artifactFrom(e), nil
}

// DeleteArtifact removes an artifact.
func (c *Client) DeleteArtifact(ctx context.Context, name string) error {
	kv, err := c.artifacts(ctx)
	if err != nil {
		return err
	}
	if err := kv.Delete(ctx, name); err != nil {
		return fmt.Errorf("sextant: delete artifact %q: %w", name, err)
	}
	return nil
}

// ArtifactChange is a change delivered to a WatchArtifact handler: the artifact
// at this revision, plus whether the change was a deletion. On a delete the
// Record is empty and Deleted is true — so a watcher can tell a removal from a
// write rather than inferring it from an empty record.
type ArtifactChange struct {
	Artifact
	Deleted bool
}

// WatchArtifact calls h on each change to name, starting with its current value
// if present. Deletes are delivered too (with Deleted set). The watch runs until
// Stop is called or ctx is cancelled, whichever comes first.
func (c *Client) WatchArtifact(ctx context.Context, name string, h func(ArtifactChange)) (Watch, error) {
	kv, err := c.artifacts(ctx)
	if err != nil {
		return nil, err
	}
	w, err := kv.Watch(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("sextant: watch artifact %q: %w", name, err)
	}
	go func() {
		for e := range w.Updates() {
			if e == nil {
				continue // marks the end of the initial replay
			}
			h(ArtifactChange{
				Artifact: artifactFrom(e),
				Deleted:  e.Operation() != jetstream.KeyValuePut,
			})
		}
	}()
	// Bridge ctx cancellation to teardown: stopping the watcher closes Updates(),
	// which ends the delivery goroutine above.
	subCtx, cancel := context.WithCancel(ctx)
	go func() {
		<-subCtx.Done()
		_ = w.Stop()
	}()
	return &artifactWatch{cancel: cancel}, nil
}

// artifactWatch tears the watch down on Stop or ctx cancellation.
type artifactWatch struct {
	cancel context.CancelFunc
}

// Stop ends the watch (idempotent). It cancels the internal context, which the
// bridge goroutine observes to stop the underlying KV watcher.
func (a *artifactWatch) Stop() error {
	a.cancel()
	return nil
}

func artifactFrom(e jetstream.KeyValueEntry) Artifact {
	return Artifact{
		Name:     e.Key(),
		Record:   e.Value(),
		Revision: e.Revision(),
		Created:  e.Created(),
	}
}

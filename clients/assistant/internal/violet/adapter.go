package violet

import (
	"context"
	"encoding/json"

	"github.com/love-lena/sextant/protocol/wire"
	"github.com/love-lena/sextant/sdk/go"
)

// sdkAdapter bridges the concrete *sextant.Client to violet's busClient
// interface. The interface stays minimal and fake-able (so the under-load test
// drives real goroutines without a bus); this adapter is the only place that
// touches the SDK's concrete Message/Subscription/Artifact types.
type sdkAdapter struct {
	c *sextant.Client
}

// NewSDKAdapter wraps a connected SDK client as a busClient.
func NewSDKAdapter(c *sextant.Client) busClient { return &sdkAdapter{c: c} }

func (a *sdkAdapter) PublishMsg(ctx context.Context, subject string, record json.RawMessage) (publishResult, error) {
	out, err := a.c.PublishMsg(ctx, subject, record)
	if err != nil {
		return publishResult{}, err
	}
	return publishResult{ID: out.ID}, nil
}

func (a *sdkAdapter) Subscribe(ctx context.Context, subject string, h func(Message), _ ...subOpt) (stopper, error) {
	sub, err := a.c.Subscribe(ctx, subject, func(m sextant.Message) {
		h(Message{
			Author:   m.Frame.Author,
			Subject:  m.Subject,
			Record:   json.RawMessage(m.Frame.Record),
			Sequence: m.Sequence,
		})
	})
	if err != nil {
		return nil, err
	}
	return sub, nil
}

func (a *sdkAdapter) GetArtifact(ctx context.Context, name string) (artifactValue, error) {
	art, err := a.c.GetArtifact(ctx, name)
	if err != nil {
		return artifactValue{}, err
	}
	return artifactValue{Name: art.Name, Record: json.RawMessage(art.Record), Revision: art.Revision}, nil
}

func (a *sdkAdapter) CreateArtifact(ctx context.Context, name string, record wire.Lexicon) (uint64, error) {
	return a.c.CreateArtifact(ctx, name, record)
}

func (a *sdkAdapter) UpdateArtifact(ctx context.Context, name string, record wire.Lexicon, expectedRev uint64) (uint64, error) {
	return a.c.UpdateArtifact(ctx, name, record, expectedRev)
}

func (a *sdkAdapter) ListArtifacts(ctx context.Context) ([]artifactInfo, error) {
	infos, err := a.c.ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]artifactInfo, 0, len(infos))
	for _, i := range infos {
		out = append(out, artifactInfo{Name: i.Name, Revision: i.Revision})
	}
	return out, nil
}

// FetchMessages implements the AC8 offline-gap replay pull path. It wraps the
// SDK's FetchMessages, converting wire.Frame → fetchedFrame. Sequence is set
// to 0 for each frame because the SDK's FetchMessages does not expose per-frame
// stream sequences in the wire.Frame type (the bus strips them before returning);
// only the batch NextCursor is available. The Sequence=0 frames are handled by
// the answerDM guard: when Sequence==0 the fine-grained live-path idempotency
// check is skipped, and the coarser replay-level `since` filter (ack.readFrom())
// covers idempotency instead. This is safe — see replay.go for details.
func (a *sdkAdapter) FetchMessages(ctx context.Context, subject string, since uint64, limit int) ([]fetchedFrame, uint64, error) {
	frames, next, err := a.c.FetchMessages(ctx, subject, since, limit)
	if err != nil {
		return nil, since, err
	}
	out := make([]fetchedFrame, 0, len(frames))
	for _, f := range frames {
		out = append(out, fetchedFrame{
			Author:   f.Author,
			Sequence: 0, // not available from FetchMessages; replay uses `since` for idempotency
			Record:   json.RawMessage(f.Record),
		})
	}
	return out, next, nil
}

func (a *sdkAdapter) ID() string        { return a.c.ID() }
func (a *sdkAdapter) Principal() string { return a.c.Principal() }

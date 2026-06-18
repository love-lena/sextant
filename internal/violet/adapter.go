package violet

import (
	"context"
	"encoding/json"

	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/wire"
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

func (a *sdkAdapter) ID() string        { return a.c.ID() }
func (a *sdkAdapter) Principal() string { return a.c.Principal() }

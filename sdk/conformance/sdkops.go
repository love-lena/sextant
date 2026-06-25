package conformance

import (
	"context"
	"encoding/json"

	"github.com/love-lena/sextant/protocol/wire"
	sextant "github.com/love-lena/sextant/sdk/go"
)

// SDKOps adapts a live SDK *Client to the Ops surface, so the exact same
// convention verb that gets recorded into a vector also runs against a real bus
// unchanged. It is the proof that Ops is a faithful subset of the SDK and not a
// parallel invention: every method here is a one-line pass-through to the
// client (wire.Lexicon is an alias for json.RawMessage, so records pass
// straight through; the artifact reads project the SDK's richer return types
// down to what a verb needs).
//
// A convention's production code uses the *Client directly; SDKOps exists for
// the conformance/e2e path that wants to drive a verb through the same Ops seam
// against a real bus. It carries no logic of its own.
type SDKOps struct {
	Client *sextant.Client
}

// NewSDKOps wraps a live client as Ops.
func NewSDKOps(c *sextant.Client) *SDKOps { return &SDKOps{Client: c} }

func (s *SDKOps) Publish(ctx context.Context, subject string, record json.RawMessage) error {
	return s.Client.Publish(ctx, subject, record)
}

func (s *SDKOps) CreateArtifact(ctx context.Context, name string, record json.RawMessage) (uint64, error) {
	return s.Client.CreateArtifact(ctx, name, wire.Lexicon(record))
}

func (s *SDKOps) UpdateArtifact(ctx context.Context, name string, record json.RawMessage, expectedRev uint64) (uint64, error) {
	return s.Client.UpdateArtifact(ctx, name, wire.Lexicon(record), expectedRev)
}

func (s *SDKOps) GetArtifact(ctx context.Context, name string) (json.RawMessage, uint64, error) {
	a, err := s.Client.GetArtifact(ctx, name)
	if err != nil {
		return nil, 0, err
	}
	return json.RawMessage(a.Record), a.Revision, nil
}

func (s *SDKOps) DeleteArtifact(ctx context.Context, name string) error {
	return s.Client.DeleteArtifact(ctx, name)
}

func (s *SDKOps) ListArtifacts(ctx context.Context) ([]string, error) {
	infos, err := s.Client.ListArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(infos))
	for _, i := range infos {
		names = append(names, i.Name)
	}
	return names, nil
}

// Compile-time assertion that SDKOps satisfies Ops.
var _ Ops = (*SDKOps)(nil)

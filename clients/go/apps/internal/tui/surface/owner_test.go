package surface

import (
	"context"
	"errors"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/tui/theme"
)

// These tests pin the owner-tag demux on the broadcast-shared messages. The
// layout broadcasts every non-key message to ALL mounted panes, and with three
// browsers a DM, a topic conversation, and an artifact reader can all be live at
// once — so a publish result or a watch failure must be claimed only by the
// surface that issued it. Without the tag, one pane's publish failure would
// footer every conversation, and one pane's success would clear another pane's
// real error.

// TestPublishedMsgOwnerDemux: a publishedMsg is claimed only by its owner — a
// failure footers only the emitting stream, and a success clears only the
// emitting stream's footer (never another pane's real error).
func TestPublishedMsgOwnerDemux(t *testing.T) {
	th, keys := theme.Dark(), theme.DefaultKeymap()
	a := NewStream(context.Background(), nil, "msg.topic.a", th, keys, WithCompose())
	b := NewStream(context.Background(), nil, "msg.topic.b", th, keys, WithCompose())

	boom := errors.New("publish rejected")
	fail := publishedMsg{owner: a, err: boom}
	a.Update(fail)
	b.Update(fail)
	if !errors.Is(a.err, boom) {
		t.Errorf("owner did not claim its own publish failure: err=%v", a.err)
	}
	if b.err != nil {
		t.Errorf("another stream claimed a foreign publish failure: err=%v", b.err)
	}

	// a's success must not clear b's real error.
	b.err = errors.New("b's real subscribe error")
	ok := publishedMsg{owner: a}
	a.Update(ok)
	b.Update(ok)
	if a.err != nil {
		t.Errorf("owner's success did not clear its own footer: err=%v", a.err)
	}
	if b.err == nil {
		t.Error("a foreign publish success cleared another pane's real error")
	}

	// An untagged result (nil owner — test-synthesized) is treated as the
	// receiving surface's own, the documented fallback the goldens rely on.
	c := NewStream(context.Background(), nil, "msg.topic.c", th, keys, WithCompose())
	c.Update(publishedMsg{err: boom})
	if !errors.Is(c.err, boom) {
		t.Errorf("untagged publish result was not claimed: err=%v", c.err)
	}
}

// TestPublishedMsgNotCrossKind: an Artifact's comment-publish result must not be
// claimed by a Stream (and vice versa) — the owner tag demuxes across surface
// kinds too, since publishedMsg is shared by both.
func TestPublishedMsgNotCrossKind(t *testing.T) {
	th, keys := theme.Dark(), theme.DefaultKeymap()
	s := NewStream(context.Background(), nil, "msg.topic.a", th, keys, WithCompose())
	art := NewArtifact(context.Background(), nil, "doc", th, keys, WithReview())

	boom := errors.New("comment rejected")
	fail := publishedMsg{owner: art, err: boom}
	s.Update(fail)
	art.Update(fail)
	if s.err != nil {
		t.Errorf("a stream claimed an artifact's comment failure: err=%v", s.err)
	}
	if !errors.Is(art.err, boom) {
		t.Errorf("the artifact did not claim its own comment failure: err=%v", art.err)
	}
}

// TestArtifactErrMsgOwnerDemux: a watch-open failure is claimed only by the
// artifact surface whose watch failed; another reader's footer stays clean.
func TestArtifactErrMsgOwnerDemux(t *testing.T) {
	th, keys := theme.Dark(), theme.DefaultKeymap()
	a := NewArtifact(context.Background(), nil, "doc-a", th, keys)
	b := NewArtifact(context.Background(), nil, "doc-b", th, keys)

	boom := errors.New("watch failed")
	fail := artifactErrMsg{owner: a, err: boom}
	a.Update(fail)
	b.Update(fail)
	if !errors.Is(a.err, boom) {
		t.Errorf("owner did not claim its own watch failure: err=%v", a.err)
	}
	if b.err != nil {
		t.Errorf("another reader claimed a foreign watch failure: err=%v", b.err)
	}

	// An untagged failure (nil owner — test-synthesized) is treated as own.
	c := NewArtifact(context.Background(), nil, "doc-c", th, keys)
	c.Update(artifactErrMsg{err: boom})
	if !errors.Is(c.err, boom) {
		t.Errorf("untagged watch failure was not claimed: err=%v", c.err)
	}
}

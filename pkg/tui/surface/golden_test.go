package surface_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/tui/busfeed"
	"github.com/love-lena/sextant/pkg/tui/surface"
	"github.com/love-lena/sextant/pkg/tui/theme"
	"github.com/love-lena/sextant/pkg/tui/widget"
	"github.com/love-lena/sextant/pkg/wire"
)

// The golden tests render each surface's View deterministically — fixed size,
// fixed synthetic state fed through the surface's own load/event messages, no
// bus, no time, no randomness — and assert it against a committed golden via
// teatest.RequireEqualOutput. A nil SDK client is safe here: the goldens never
// call Init or any path that dereferences the client (they feed state directly).
// Regenerate with:
//
//	go test ./pkg/tui/surface -update
//
// Sizes are the OUTER box dimensions; innerOf converts to the inner content area
// the surface is sized to, exactly as the dash's layout engine does. The layout
// owns the Box chrome, so the goldens wrap the surface's inner View in Box here.

const (
	boxOverheadW = 4
	boxOverheadH = 2
)

func innerOf(w, h int) (int, int) { return w - boxOverheadW, h - boxOverheadH }

func fixedDark() theme.Theme { return theme.Dark() }

// box wraps a surface's inner content in the same Box chrome the layout draws, so
// a golden captures what the operator actually sees.
func box(t theme.Theme, s surface.Surface, focus widget.Focus, w, h int) string {
	iw, ih := innerOf(w, h)
	s.SetSize(iw, ih)
	s.SetFocus(focus)
	return widget.Box(t, focus, s.Title(), t.RoleHue(theme.RoleHuman), s.View(), w, h)
}

func sampleClients() []sextant.ClientInfo {
	return []sextant.ClientInfo{
		{ID: "01CLIENTLENA", DisplayName: "lena", Kind: theme.RoleHuman, Online: true},
		{ID: "01CLIENTCOORD", DisplayName: "coordinator-1", Kind: theme.RoleCoordinator, Online: true},
		{ID: "01CLIENTDISP", DisplayName: "dispatcher-1", Kind: theme.RoleDispatcher, Online: false},
		{ID: "01CLIENTALPHA", DisplayName: "agent-alpha", Kind: theme.RoleAgent, Online: true},
		{ID: "01CLIENTBETA", DisplayName: "agent-beta", Kind: theme.RoleAgent, Online: false},
	}
}

// --- message stream ---

// chatEvent builds a synthetic received chat.message from author, as the bus
// would echo it back on the subscription.
func chatEvent(author, text string) busfeed.EventMsg {
	rec, _ := json.Marshal(map[string]string{"$type": "chat.message", "text": text})
	return busfeed.EventMsg{Message: sextant.Message{
		Frame:   wire.Frame{ID: "01" + author, Author: author, Kind: wire.KindMessage, Epoch: wire.Epoch, Record: rec},
		Subject: "msg.topic.plan",
		BusTime: time.Unix(0, 0),
	}}
}

// sampleAuthors maps the synthetic author ids to display names + roles, the
// presence-derived map the dash hands the stream so authors render in role hue.
func sampleAuthors() map[string]surface.Author {
	return map[string]surface.Author{
		"lena":          {Name: "lena", Role: theme.RoleHuman},
		"coordinator-1": {Name: "coordinator-1", Role: theme.RoleCoordinator},
		"agent-alpha":   {Name: "agent-alpha", Role: theme.RoleAgent},
	}
}

func feedStream(s *surface.Stream) {
	for _, e := range []busfeed.EventMsg{
		chatEvent("lena", "let's get the dash building"),
		chatEvent("coordinator-1", "spinning up agent-alpha for the toolkit"),
		chatEvent("agent-alpha", "accepted — starting on theme + widgets"),
		chatEvent("agent-alpha", "palette resolved, goldens green"),
		chatEvent("coordinator-1", "nice, eyeball the gallery"),
	} {
		s.Update(e)
	}
}

func TestStreamTailGolden(t *testing.T) {
	th := fixedDark()
	for _, tc := range []struct {
		name  string
		focus widget.Focus
		feed  bool
	}{
		{"empty", widget.FocusSelected, false},
		{"idle", widget.FocusIdle, true},
		{"selected", widget.FocusSelected, true},
		{"active", widget.FocusActive, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap(), surface.WithAuthors(sampleAuthors()))
			if tc.feed {
				feedStream(s)
			}
			out := box(th, s, tc.focus, 48, 9)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestStreamComposeGolden(t *testing.T) {
	th := fixedDark()
	for _, tc := range []struct {
		name  string
		focus widget.Focus
	}{
		{"compose_selected", widget.FocusSelected}, // dim hint
		{"compose_active", widget.FocusActive},     // live input
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap(), surface.WithCompose(), surface.WithAuthors(sampleAuthors()))
			feedStream(s)
			// size before feeding keystrokes so the input width is set
			iw, ih := innerOf(48, 10)
			s.SetSize(iw, ih)
			s.SetFocus(tc.focus)
			if tc.focus == widget.FocusActive {
				typeInto(s, "ship it")
			}
			out := widget.Box(th, tc.focus, s.Title(), th.RoleHue(theme.RoleHuman), s.View(), 48, 10)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestStreamDroppedGolden(t *testing.T) {
	th := fixedDark()
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap())
	iw, ih := innerOf(48, 9)
	s.SetSize(iw, ih)
	s.SetFocus(widget.FocusSelected)
	s.Update(chatEvent("lena", "before the gap"))
	s.Update(busfeed.DroppedMsg{N: 7})
	s.Update(chatEvent("agent-alpha", "after the gap"))
	out := widget.Box(th, widget.FocusSelected, s.Title(), th.RoleHue(theme.RoleHuman), s.View(), 48, 9)
	teatest.RequireEqualOutput(t, []byte(out))
}

// --- artifact ---

func sampleDocument() sextant.Artifact {
	rec, _ := json.Marshal(map[string]string{
		"$type": "document",
		"title": "Dash build plan",
		"body":  "The dash assembles **pane-surfaces** into a layout.\n\n- presence\n- message stream\n- artifact\n\nDetail-on-demand is toggled, never an always-on column.",
	})
	return sextant.Artifact{Name: "dash-plan", Record: wire.Lexicon(rec), Revision: 3}
}

func TestArtifactReaderGolden(t *testing.T) {
	th := fixedDark()
	for _, tc := range []struct {
		name  string
		focus widget.Focus
		feed  bool
	}{
		{"empty", widget.FocusSelected, false},
		{"idle", widget.FocusIdle, true},
		{"selected", widget.FocusSelected, true},
		{"active", widget.FocusActive, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := surface.NewArtifact(context.Background(), nil, "dash-plan", th, theme.DefaultKeymap())
			iw, ih := innerOf(48, 14)
			a.SetSize(iw, ih)
			if tc.feed {
				a.Update(surface.ArtifactLoadedMsg{Artifact: sampleDocument()})
			}
			a.SetFocus(tc.focus)
			out := widget.Box(th, tc.focus, a.Title(), th.KindHue(theme.KindArtifactUpdate), a.View(), 48, 14)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

func TestArtifactReviewGolden(t *testing.T) {
	th := fixedDark()
	for _, tc := range []struct {
		name  string
		focus widget.Focus
	}{
		{"review_selected", widget.FocusSelected},
		{"review_active", widget.FocusActive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := surface.NewArtifact(context.Background(), nil, "dash-plan", th, theme.DefaultKeymap(), surface.WithReview())
			iw, ih := innerOf(48, 14)
			a.SetSize(iw, ih)
			a.Update(surface.ArtifactLoadedMsg{Artifact: sampleDocument()})
			a.SetFocus(tc.focus)
			if tc.focus == widget.FocusActive {
				typeIntoArtifact(a, "tighten the intro")
			}
			out := widget.Box(th, tc.focus, a.Title(), th.KindHue(theme.KindArtifactUpdate), a.View(), 48, 14)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

// --- error footers (fail-loud: a captured error must render, never swallow) ---

// TestErrorFootersGolden pins that each surface renders its captured error in a
// footer line, driven through the surface's own (test-only) error messages. The
// footer appears below any content/compose row — proof the error is visible, not
// swallowed.
func TestErrorFootersGolden(t *testing.T) {
	th := fixedDark()

	t.Run("stream_subscribe_error", func(t *testing.T) {
		s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap(), surface.WithAuthors(sampleAuthors()))
		feedStream(s)
		iw, ih := innerOf(48, 9)
		s.SetSize(iw, ih)
		s.SetFocus(widget.FocusSelected)
		s.Update(surface.NewFeedErrMsg(errors.New("subscribe failed")))
		out := widget.Box(th, widget.FocusSelected, s.Title(), th.RoleHue(theme.RoleHuman), s.View(), 48, 9)
		teatest.RequireEqualOutput(t, []byte(out))
	})

	t.Run("stream_compose_publish_error", func(t *testing.T) {
		s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap(), surface.WithCompose(), surface.WithAuthors(sampleAuthors()))
		feedStream(s)
		iw, ih := innerOf(48, 10)
		s.SetSize(iw, ih)
		s.SetFocus(widget.FocusActive)
		s.Update(surface.NewPublishedErrMsg(errors.New("publish rejected")))
		out := widget.Box(th, widget.FocusActive, s.Title(), th.RoleHue(theme.RoleHuman), s.View(), 48, 10)
		teatest.RequireEqualOutput(t, []byte(out))
	})

	t.Run("clients_browser_fetch_error", func(t *testing.T) {
		cb := surface.NewClientsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
		cb.Update(surface.ClientsLoadedMsg{Clients: sampleClients()}) // last good snapshot stays
		cb.Update(surface.NewClientsErrMsg(errors.New("bus unreachable")))
		out := box(th, cb, widget.FocusSelected, 30, 9)
		teatest.RequireEqualOutput(t, []byte(out))
	})

	t.Run("artifacts_browser_fetch_error", func(t *testing.T) {
		ab := surface.NewArtifactsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
		ab.Update(surface.ArtifactsLoadedMsg{Artifacts: fixedArtifacts()}) // last good snapshot stays
		ab.Update(surface.NewArtifactsErrMsg(errors.New("artifact.list failed")))
		out := box(th, ab, widget.FocusSelected, 32, 9)
		teatest.RequireEqualOutput(t, []byte(out))
	})

	t.Run("artifact_fetch_error", func(t *testing.T) {
		a := surface.NewArtifact(context.Background(), nil, "dash-plan", th, theme.DefaultKeymap())
		iw, ih := innerOf(48, 14)
		a.SetSize(iw, ih)
		a.Update(surface.ArtifactLoadedMsg{Artifact: sampleDocument()})
		a.Update(surface.NewArtifactErrMsg(errors.New("artifact not found")))
		a.SetFocus(widget.FocusSelected)
		out := widget.Box(th, widget.FocusSelected, a.Title(), th.KindHue(theme.KindArtifactUpdate), a.View(), 48, 14)
		teatest.RequireEqualOutput(t, []byte(out))
	})
}

// TestBrowserErrorFooterFullListGolden pins that each browser's error footer is
// visible even when the list fills the entire allocated height. The inner height
// is set to exactly the number of rows in the snapshot — no spare rows — so
// without the relayoutList fix the footer would be clipped. With it the list is
// one row shorter and the footer appears below.
func TestBrowserErrorFooterFullListGolden(t *testing.T) {
	th := fixedDark()

	t.Run("clients_full_list", func(t *testing.T) {
		cb := surface.NewClientsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
		// 5 clients; size inner h to exactly 5 so the list is full
		cb.Update(surface.ClientsLoadedMsg{Clients: sampleClients()})
		cb.Update(surface.NewClientsErrMsg(errors.New("bus unreachable")))
		out := box(th, cb, widget.FocusSelected, 30, 7) // innerOf(30,7) = (26,5)
		teatest.RequireEqualOutput(t, []byte(out))
	})

	t.Run("artifacts_full_list", func(t *testing.T) {
		ab := surface.NewArtifactsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
		// 3 artifacts; size inner h to exactly 3 so the list is full
		ab.Update(surface.ArtifactsLoadedMsg{Artifacts: fixedArtifacts()})
		ab.Update(surface.NewArtifactsErrMsg(errors.New("artifact.list failed")))
		out := box(th, ab, widget.FocusSelected, 32, 5) // innerOf(32,5) = (28,3)
		teatest.RequireEqualOutput(t, []byte(out))
	})

	t.Run("topics_full_list", func(t *testing.T) {
		tb := surface.NewTopicsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
		// 3 topics; size inner h to exactly 3 so the list is full
		for _, e := range fixedTopicEvents() {
			tb.Update(e)
		}
		tb.Update(surface.NewTopicsErrMsg(errors.New("discovery feed dropped")))
		out := box(th, tb, widget.FocusSelected, 28, 5) // innerOf(28,5) = (24,3)
		teatest.RequireEqualOutput(t, []byte(out))
	})
}

// typeInto feeds a string into a stream's active compose, key by key.
func typeInto(s *surface.Stream, text string) {
	for _, r := range text {
		s.Update(keyRune(r))
	}
}

func typeIntoArtifact(a *surface.Artifact, text string) {
	for _, r := range text {
		a.Update(keyRune(r))
	}
}

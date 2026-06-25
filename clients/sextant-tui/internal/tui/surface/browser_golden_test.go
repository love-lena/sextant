package surface_test

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/busfeed"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/surface"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
	"github.com/love-lena/sextant/sdk/go"
)

// The browser golden tests render each ADR-0024 browser's View deterministically —
// fixed size, fixed synthetic state fed through the browser's own load/event
// messages, no bus — and assert it against a committed golden. Each browser is
// covered at the list level (its data rendered as rows, in several focus states)
// and at the detail level (post-Enter: the same Stream/Artifact detail the browser
// opens, fed synthetic events so the conversation/reader renders without a bus).
// A nil SDK client is safe: opening a row builds the detail and returns its Init
// command (a feed Subscribe / artifact Watch) which the golden never RUNS — it
// feeds the detail synthetic events directly, exactly as the surface goldens do.
// Regenerate with: go test ./pkg/tui/surface -update

func fixedArtifacts() []sextant.ArtifactInfo {
	return []sextant.ArtifactInfo{
		{Name: "dash-plan", Revision: 3, Updated: time.Unix(0, 0)},
		{Name: "protocol-notes", Revision: 12, Updated: time.Unix(0, 0)},
		{Name: "scratch", Revision: 0, Updated: time.Unix(0, 0)}, // unstamped: no rev cue
	}
}

func fixedTopicEvents() []busfeed.EventMsg {
	return []busfeed.EventMsg{
		topicEvent("plan", "lena", "kick off the dash build"),
		topicEvent("build", "agent-alpha", "widgets landed"),
		topicEvent("review", "coordinator-1", "eyeball the gallery"),
	}
}

// topicEvent builds a synthetic received chat.message on a topic subject, as the
// discovery feed would replay it — the browser learns the topic from the subject.
func topicEvent(topic, author, text string) busfeed.EventMsg {
	e := chatEvent(author, text)
	e.Message.Subject = "msg.topic." + topic
	return e
}

// --- clients browser ---

func TestClientsBrowserGolden(t *testing.T) {
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
			cb := surface.NewClientsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
			if tc.feed {
				cb.Update(surface.ClientsLoadedMsg{Clients: sampleClients()})
			}
			out := box(th, cb, tc.focus, 28, 9)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

// TestClientsBrowserDetailGolden pins the DM detail the clients browser opens:
// feed the directory, Enter to open a direct conversation, feed it a couple of
// chat events, and render — proof the browser opens the Stream detail in place.
func TestClientsBrowserDetailGolden(t *testing.T) {
	th := fixedDark()
	cb := surface.NewClientsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
	iw, ih := innerOf(40, 10)
	cb.SetSize(iw, ih)
	cb.SetFocus(widget.FocusActive)
	cb.Update(surface.ClientsLoadedMsg{Clients: sampleClients()})
	cb.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open the DM for the cursor row
	for _, e := range []busfeed.EventMsg{
		chatEvent("agent-alpha", "on it — pulling the latest"),
		chatEvent("lena", "thanks, ping when the build is green"),
	} {
		cb.Update(e)
	}
	out := widget.Box(th, widget.FocusActive, cb.Title(), th.RoleHue(theme.RoleHuman), cb.View(), 40, 10)
	teatest.RequireEqualOutput(t, []byte(out))
}

// --- artifacts browser ---

func TestArtifactsBrowserGolden(t *testing.T) {
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
			ab := surface.NewArtifactsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
			if tc.feed {
				ab.Update(surface.ArtifactsLoadedMsg{Artifacts: fixedArtifacts()})
			}
			out := box(th, ab, tc.focus, 32, 9)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

// TestArtifactsBrowserDetailGolden pins the reader detail the artifacts browser
// opens: feed the directory, Enter to open the reader, feed it a synthetic
// document, and render — proof the browser opens the Artifact detail in place.
func TestArtifactsBrowserDetailGolden(t *testing.T) {
	th := fixedDark()
	ab := surface.NewArtifactsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
	iw, ih := innerOf(48, 14)
	ab.SetSize(iw, ih)
	ab.SetFocus(widget.FocusActive)
	ab.Update(surface.ArtifactsLoadedMsg{Artifacts: fixedArtifacts()})
	ab.Update(tea.KeyMsg{Type: tea.KeyEnter})                        // open the reader for the cursor row
	ab.Update(surface.ArtifactLoadedMsg{Artifact: sampleDocument()}) // feed the reader its document
	out := widget.Box(th, widget.FocusActive, ab.Title(), th.KindHue(theme.KindArtifactUpdate), ab.View(), 48, 14)
	teatest.RequireEqualOutput(t, []byte(out))
}

// --- topics browser ---

func TestTopicsBrowserGolden(t *testing.T) {
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
			tb := surface.NewTopicsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
			if tc.feed {
				for _, e := range fixedTopicEvents() {
					tb.Update(e) // discovery: each event teaches the browser a topic name
				}
			}
			out := box(th, tb, tc.focus, 28, 9)
			teatest.RequireEqualOutput(t, []byte(out))
		})
	}
}

// TestTopicsBrowserDetailGolden pins the conversation detail the topics browser
// opens: discover topics from synthetic events, Enter to open the selected topic's
// conversation, feed it a couple of chat events, and render — proof the browser
// opens the Stream detail in place.
func TestTopicsBrowserDetailGolden(t *testing.T) {
	th := fixedDark()
	tb := surface.NewTopicsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
	iw, ih := innerOf(48, 10)
	tb.SetSize(iw, ih)
	tb.SetFocus(widget.FocusActive)
	for _, e := range fixedTopicEvents() {
		tb.Update(e)
	}
	tb.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open the cursor topic's conversation
	for _, e := range []busfeed.EventMsg{
		chatEvent("lena", "let's get the dash building"),
		chatEvent("agent-alpha", "accepted — starting now"),
	} {
		tb.Update(e)
	}
	out := widget.Box(th, widget.FocusActive, tb.Title(), th.KindHue(theme.KindChat), tb.View(), 48, 10)
	teatest.RequireEqualOutput(t, []byte(out))
}

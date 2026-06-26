package surface_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/busfeed"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/surface"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/sdk/go"
)

// deferredNotice builds the wrapped non-fatal notice the SDK's OnError emits
// for a transport-failed resume (sextant.ErrResumeDeferred), as busfeed routes
// it into a ResumeDeferredMsg.
func deferredNotice(subject string) error {
	return fmt.Errorf("%w: subscription on %q delivers nothing until then: timeout", sextant.ErrResumeDeferred, subject)
}

// noticePhrase is the stable fragment of the sentinel's message the footer must
// carry for the stall to be visible.
const noticePhrase = "resume deferred"

// TestStreamResumeDeferredNoticeShowsAndClears pins the stream's handling of
// the non-fatal resume notice: the surface renders a transient footer, KEEPS
// PUMPING (a deferred resume is recoverable — treating it as the terminal
// ErrMsg permanently killed the pane, the regression behind this test), and
// clears the footer on the next delivered event (frames flowing again means
// the deferred resume succeeded).
func TestStreamResumeDeferredNoticeShowsAndClears(t *testing.T) {
	th := theme.Dark()
	s := surface.NewStream(context.Background(), nil, "msg.topic.plan", th, theme.DefaultKeymap())
	s.SetSize(60, 8)

	cmd := s.Update(busfeed.ResumeDeferredMsg{Err: deferredNotice("msg.topic.plan")})
	if cmd == nil {
		t.Fatal("ResumeDeferredMsg returned a nil cmd: the pump died on a recoverable notice")
	}
	if view := s.View(); !strings.Contains(view, noticePhrase) {
		t.Errorf("notice not visible after ResumeDeferredMsg (silently swallowed); view:\n%s", view)
	}

	// The next delivered event proves the stall is over: the notice auto-clears.
	s.Update(chatEvent("lena", "back online"))
	if view := s.View(); strings.Contains(view, noticePhrase) {
		t.Errorf("notice still showing after an event was delivered (should auto-clear); view:\n%s", view)
	}
}

// TestTopicsBrowserResumeDeferredNoticeShowsAndClears pins the same contract on
// the discovery feed: the browser claims the notice (so it is not misrouted to
// an open detail), keeps the discovery pump alive, shows the footer at the
// list, and clears it when discovery frames flow again.
func TestTopicsBrowserResumeDeferredNoticeShowsAndClears(t *testing.T) {
	th := theme.Dark()
	b := surface.NewTopicsBrowser(context.Background(), nil, th, theme.DefaultKeymap())
	b.SetSize(60, 10)

	cmd := b.Update(busfeed.ResumeDeferredMsg{Err: deferredNotice("msg.topic.>")})
	if cmd == nil {
		t.Fatal("ResumeDeferredMsg returned a nil cmd: the discovery pump died on a recoverable notice")
	}
	if view := b.View(); !strings.Contains(view, noticePhrase) {
		t.Errorf("notice not visible on the topics list (silently swallowed); view:\n%s", view)
	}

	b.Update(topicEvent("plan", "lena", "kick off"))
	if view := b.View(); strings.Contains(view, noticePhrase) {
		t.Errorf("notice still showing after a discovery frame arrived (should auto-clear); view:\n%s", view)
	}
}

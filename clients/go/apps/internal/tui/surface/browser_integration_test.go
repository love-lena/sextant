package surface_test

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/surface"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/theme"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/widget"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/protocol/wire"
)

// These integration tests stand up an embedded bus and prove the three ADR-0024
// browsers' live behaviour the goldens cannot: topics discovered client-side from
// the msg.topic.> replay, a DM opened on a client's direct subject round-tripping,
// and artifacts listed via artifact.list then read live. Every wait is bounded so
// a wedged bus fails the test instead of hanging (fail-loud); each browser's Stop
// tears down its feeds/watches, which the package goleak check proves clean.

// drivePump keeps feeding a surface the result of cmd, then whatever it returns,
// running on a deadline and checking done after each step — the same walk as
// integration_test.go's driveUntil, but it RETURNS the last pending command so a
// caller can resume the same live feed pump after an interleaved action (a publish
// that round-trips back through the still-open feed). It expands a tea.Batch and
// re-arms a feed pump that delivered nothing yet by polling: when the queue
// empties before done, it parks briefly and re-issues the surface's pending step.
func drivePump(t *testing.T, s surface.Surface, cmd tea.Cmd, done func() bool) tea.Cmd {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	queue := []tea.Cmd{cmd}
	for !done() && time.Now().Before(deadline) {
		if len(queue) == 0 {
			t.Fatal("ran out of commands before the condition held")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		switch msg := runStepDeadline(t, next, time.Until(deadline)).(type) {
		case tea.BatchMsg:
			queue = append(append([]tea.Cmd{}, msg...), queue...)
		case nil:
		default:
			if c := s.Update(msg); c != nil {
				queue = append(queue, c)
			}
		}
	}
	if !done() {
		t.Fatalf("condition never held within deadline; view:\n%s", stripANSI(s.View()))
	}
	if len(queue) > 0 {
		return queue[0]
	}
	return nil
}

// runStepDeadline runs a tea.Cmd to completion bounded by d. A non-positive d
// fails immediately, so a caller past its deadline does not block.
func runStepDeadline(t *testing.T, cmd tea.Cmd, d time.Duration) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	if d <= 0 {
		t.Fatal("deadline passed before the step could run")
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case m := <-out:
		return m
	case <-time.After(d):
		t.Fatal("tea.Cmd did not return within the deadline")
		return nil
	}
}

// TestTopicsBrowserDiscoversAndOpens pins the topics browser end to end: publish a
// message to two distinct topics, drive the discovery feed until both topic names
// appear in the list, then open one and confirm its conversation renders the
// message and a freshly sent line round-trips back through the bus.
func TestTopicsBrowserDiscoversAndOpens(t *testing.T) {
	b := startBus(t)
	c := connect(t, b, "lena", "human")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	for _, topic := range []string{"plan", "build"} {
		pctx, pcancel := context.WithTimeout(t.Context(), 5*time.Second)
		if err := c.Publish(pctx, sx.TopicSubject(topic), mustChat(t, "hello "+topic)); err != nil {
			pcancel()
			t.Fatalf("Publish(%s): %v", topic, err)
		}
		pcancel()
	}

	tb := surface.NewTopicsBrowser(ctx, c, theme.Dark(), theme.DefaultKeymap())
	defer tb.Stop()
	tb.SetSize(40, 10)
	tb.SetFocus(widget.FocusActive)

	// Discovery: drive the msg.topic.> feed until BOTH topics appear in the list.
	drivePump(t, tb, tb.Init(), func() bool {
		v := stripANSI(tb.View())
		return strings.Contains(v, "plan") && strings.Contains(v, "build")
	})

	// "build" sorts above "plan", but the selection is keyed by identity: the
	// cursor stays on "plan" (the first topic discovered, selected when the
	// list appeared) rather than sliding onto whichever row took index 0. Open
	// it; its conversation must render its seeded message. Keep the live pump
	// for the round-trip below.
	pump := drivePump(t, tb, enter(tb), func() bool {
		return strings.Contains(stripANSI(tb.View()), "hello plan")
	})

	// A line composed in the open conversation round-trips back through the bus
	// (no optimistic echo). Type it, press Enter (returns the publish cmd), run
	// that, then resume the SAME feed pump until the line appears.
	const sent = "topic round-trip"
	typeRunes(tb, sent)
	pub := tb.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pub == nil {
		t.Fatal("Enter in the open conversation did not return a publish command")
	}
	if msg := runStep(t, pub); msg != nil {
		tb.Update(msg)
	}
	drivePump(t, tb, pump, func() bool {
		return strings.Contains(stripANSI(tb.View()), sent)
	})
}

// TestClientsBrowserOpensDM pins the clients browser end to end: with a couple of
// minted clients, drive its ListClients fetch until they appear in the list, open
// the selected one, and confirm a line sent in the opened DM round-trips back over
// the client's direct subject (msg.client.<id>).
func TestClientsBrowserOpensDM(t *testing.T) {
	b := startBus(t)
	c := connect(t, b, "lena", "human")
	connect(t, b, "agent-alpha", "agent")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cb := surface.NewClientsBrowser(ctx, c, theme.Dark(), theme.DefaultKeymap())
	defer cb.Stop()
	cb.SetSize(40, 10)
	cb.SetFocus(widget.FocusActive)

	drivePump(t, cb, cb.Init(), func() bool {
		v := stripANSI(cb.View())
		return strings.Contains(v, "lena") && strings.Contains(v, "agent-alpha")
	})

	// "agent-alpha" sorts before "lena", so the cursor rests on it. Open the DM:
	// enter returns the opened Stream's Subscribe command (the feed's open). The
	// feed delivers with DeliverAll, so a line published before the subscription is
	// fully live still round-trips back — the publish need not wait on the
	// handshake. Type + Enter to publish, then drive BOTH the feed open and the
	// publish until the sent line round-trips back over msg.client.<id>.
	openCmd := enter(cb)
	const sent = "direct message round-trip"
	typeRunes(cb, sent)
	pubCmd := cb.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pubCmd == nil {
		t.Fatal("Enter in the DM did not return a publish command")
	}
	drivePump(t, cb, tea.Batch(openCmd, pubCmd), func() bool {
		return strings.Contains(stripANSI(cb.View()), sent)
	})
}

// TestArtifactsBrowserListsAndReads pins the artifacts browser end to end: create
// two artifacts, drive ListArtifacts until both appear in the list, open one, and
// confirm the reader renders it — then update it and confirm the reader refreshes
// over its live watch (without re-opening).
func TestArtifactsBrowserListsAndReads(t *testing.T) {
	b := startBus(t)
	c := connect(t, b, "lena", "human")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	for _, d := range []struct{ name, title, body string }{
		{"alpha-doc", "Alpha title", "the **alpha** body"},
		{"beta-doc", "Beta title", "the beta body"},
	} {
		cctx, ccancel := context.WithTimeout(t.Context(), 5*time.Second)
		if _, err := c.CreateArtifact(cctx, d.name, wire.Lexicon(mustDoc(t, d.title, d.body))); err != nil {
			ccancel()
			t.Fatalf("CreateArtifact(%s): %v", d.name, err)
		}
		ccancel()
	}

	ab := surface.NewArtifactsBrowser(ctx, c, theme.Dark(), theme.DefaultKeymap())
	defer ab.Stop()
	ab.SetSize(50, 12)
	ab.SetFocus(widget.FocusActive)

	drivePump(t, ab, ab.Init(), func() bool {
		v := stripANSI(ab.View())
		return strings.Contains(v, "alpha-doc") && strings.Contains(v, "beta-doc")
	})

	// "alpha-doc" sorts first; open it and confirm the reader renders its content.
	// Keep the live watch pump for the live-update below.
	pump := drivePump(t, ab, enter(ab), func() bool {
		return strings.Contains(stripANSI(ab.View()), "Alpha title")
	})

	// Update the open artifact; the reader's live watch must refresh it without a
	// re-open. Resume the same watch pump until the new content shows.
	gctx, gcancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer gcancel()
	cur, err := c.GetArtifact(gctx, "alpha-doc")
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	uctx, ucancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer ucancel()
	if _, err := c.UpdateArtifact(uctx, "alpha-doc", wire.Lexicon(mustDoc(t, "Alpha revised", "the new body")), cur.Revision); err != nil {
		t.Fatalf("UpdateArtifact: %v", err)
	}
	drivePump(t, ab, pump, func() bool {
		return strings.Contains(stripANSI(ab.View()), "Alpha revised")
	})
}

// enter sends an Enter key to a surface, the press the browser opens a row on.
func enter(s surface.Surface) tea.Cmd { return s.Update(tea.KeyMsg{Type: tea.KeyEnter}) }

// typeRunes feeds a string into a surface's active compose, key by key.
func typeRunes(s surface.Surface, text string) {
	for _, r := range text {
		s.Update(keyRune(r))
	}
}

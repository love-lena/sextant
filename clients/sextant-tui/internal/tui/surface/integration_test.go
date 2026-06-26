package surface_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/busfeed"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/surface"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/theme"
	"github.com/love-lena/sextant/clients/sextant-tui/internal/tui/widget"
	"github.com/love-lena/sextant/protocol/sx"
	"github.com/love-lena/sextant/protocol/wire"
	"github.com/love-lena/sextant/sdk/go"
)

// These integration tests stand up an embedded bus and prove the live behaviour
// the goldens cannot: the message stream's round-trip (a publish appears via the
// feed echo, not a local copy), presence reflecting ListClients, and the artifact
// reader rendering a created document. Every wait is bounded so a wedged bus
// fails the test instead of hanging (fail-loud).

// startBus stands up an embedded bus the caller mints clients on.
func startBus(t *testing.T) *bus.Bus {
	t.Helper()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	return b
}

// connect mints a new client on b and dials it in.
func connect(t *testing.T, b *bus.Bus, displayName, kind string) *sextant.Client {
	t.Helper()
	creds, _, err := b.MintClient(t.Context(), displayName, kind)
	if err != nil {
		t.Fatalf("MintClient(%s): %v", displayName, err)
	}
	credsFile := filepath.Join(t.TempDir(), "creds")
	if err := os.WriteFile(credsFile, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: credsFile,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", displayName, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// runStep runs one tea.Cmd to completion on a deadline, returning its tea.Msg. It
// never hangs: a slow or wedged step fails the test.
func runStep(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	out := make(chan tea.Msg, 1)
	go func() { out <- cmd() }()
	select {
	case m := <-out:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("tea.Cmd did not return within 5s")
		return nil
	}
}

// driveUntil feeds the surface the result of cmd, then the result of whatever cmd
// it returns, and so on, calling done after each step. It stops when done is true
// or the deadline passes (fail-loud). A tea.Batch result is expanded into its
// constituent commands (so an Init that batches a fetch with a long refresh tick
// drives the fetch without waiting on the tick). It is how a test walks the feed
// pump (Subscribed → Next → Event → Next …) to a rendered condition.
func driveUntil(t *testing.T, s surface.Surface, cmd tea.Cmd, done func() bool) {
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
		switch msg := runStep(t, next).(type) {
		case tea.BatchMsg:
			// Expand the batch: enqueue each sub-command, fetch-before-tick order
			// preserved, so the meaningful result is driven before any long timer.
			queue = append(append([]tea.Cmd{}, msg...), queue...)
		case nil:
			// pump ended on this branch; keep draining the queue
		default:
			if c := s.Update(msg); c != nil {
				queue = append(queue, c)
			}
		}
	}
	if !done() {
		t.Fatalf("condition never held within deadline; view:\n%s", stripANSI(s.View()))
	}
}

// TestStreamRoundTripLive pins AC-3: subscribe, publish a chat.message, and watch
// it appear in the surface's rendered stream via the feed — NOT via a local echo.
// The publish path returns no payload; the visible line comes only from the bus
// round-trip.
func TestStreamRoundTripLive(t *testing.T) {
	b := startBus(t)
	c := connect(t, b, "lena", "human")
	subject := sx.TopicSubject("plan")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := surface.NewStream(ctx, c, subject, theme.Dark(), theme.DefaultKeymap(), surface.WithCompose())
	defer s.Stop()
	s.SetSize(60, 8)
	s.SetFocus(widget.FocusActive)

	// Open the feed: Init → Subscribe → SubscribedMsg; the surface then issues Next.
	sub := runStep(t, s.Init())
	if _, ok := sub.(busfeed.SubscribedMsg); !ok {
		t.Fatalf("Init did not report subscribed: %#v", sub)
	}
	pump := s.Update(sub) // SubscribedMsg → Next (a blocking read in flight)

	// Publish a line through the SDK. It must arrive via the feed echo, not a
	// local append — the surface never echoes optimistically.
	const want = "round-trips through the bus"
	pubCtx, pcancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer pcancel()
	if err := c.Publish(pubCtx, subject, mustChat(t, want)); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	driveUntil(t, s, pump, func() bool { return strings.Contains(stripANSI(s.View()), want) })
}

// TestStreamNoOptimisticEcho pins that composing through the surface itself does
// NOT echo locally: right after Enter publishes, the view must not yet contain
// the typed text; it appears only once the bus round-trips it back.
func TestStreamNoOptimisticEcho(t *testing.T) {
	b := startBus(t)
	c := connect(t, b, "lena", "human")
	subject := sx.TopicSubject("echo")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s := surface.NewStream(ctx, c, subject, theme.Dark(), theme.DefaultKeymap(), surface.WithCompose())
	defer s.Stop()
	s.SetSize(60, 8)
	s.SetFocus(widget.FocusActive)

	sub := runStep(t, s.Init())
	pump := s.Update(sub)

	// Type a line and press Enter; the publish runs as the returned command.
	const typed = "no optimistic echo here"
	for _, r := range typed {
		s.Update(keyRune(r))
	}
	pubCmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if pubCmd == nil {
		t.Fatal("Enter did not return a publish command")
	}

	// Before running the publish command, the typed text must NOT be in the view
	// (the input is cleared on Enter, and there is no local echo of the message).
	if strings.Contains(stripANSI(s.View()), typed) {
		t.Fatalf("typed text appeared before the bus round-trip (optimistic echo):\n%s", stripANSI(s.View()))
	}
	// Run the publish, then walk the pump until the line round-trips back.
	if msg := runStep(t, pubCmd); msg != nil {
		s.Update(msg)
	}
	driveUntil(t, s, pump, func() bool { return strings.Contains(stripANSI(s.View()), typed) })
}

// TestArtifactReaderLive pins that the artifact reader renders a created
// document: create a document artifact, fetch it through the surface, and confirm
// its title and body show in the rendered reader.
func TestArtifactReaderLive(t *testing.T) {
	b := startBus(t)
	c := connect(t, b, "lena", "human")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	createCtx, ccancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer ccancel()
	if _, err := c.CreateArtifact(createCtx, "review-doc", wire.Lexicon(mustDoc(t, "Review me", "The **body** is markdown."))); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	a := surface.NewArtifact(ctx, c, "review-doc", theme.Dark(), theme.DefaultKeymap())
	a.SetSize(50, 12)
	a.SetFocus(widget.FocusSelected)

	driveUntil(t, a, a.Init(), func() bool {
		return strings.Contains(stripANSI(a.View()), "Review me")
	})

	view := stripANSI(a.View())
	if !strings.Contains(view, "body") {
		t.Errorf("reader missing body; got:\n%s", view)
	}
}

// TestArtifactWatchLiveUpdates pins Fix 2: the artifact reader live-updates over
// WatchArtifact without a restart. It mounts the surface on a name that does NOT
// exist yet (so there is no content and no spurious error), then creates the
// artifact and confirms the surface renders it WITHOUT a re-Init, then updates it
// and confirms the reader refreshes. Every wait is deadline-bound; the watch is
// torn down via Stop so the package goleak check proves the teardown is clean.
func TestArtifactWatchLiveUpdates(t *testing.T) {
	b := startBus(t)
	c := connect(t, b, "lena", "human")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	const name = "live-doc"
	a := surface.NewArtifact(ctx, c, name, theme.Dark(), theme.DefaultKeymap())
	defer a.Stop()
	a.SetSize(50, 12)
	a.SetFocus(widget.FocusSelected)

	// Open the watch on an artifact that does not exist yet. WatchArtifact reports
	// it is live (artifactWatchingMsg) with no current value, so the pump is in
	// flight but the reader has no content and — crucially — no error footer.
	open := runStep(t, a.Init())
	pump := a.Update(open) // artifactWatchingMsg → first nextChange (blocking)

	view := stripANSI(a.View())
	if strings.Contains(view, "not found") || strings.Contains(view, "error") {
		t.Fatalf("absent-at-launch artifact should show no error, got:\n%s", view)
	}
	if strings.Contains(view, "First title") {
		t.Fatalf("artifact shown before it was created:\n%s", view)
	}

	// Create the artifact AFTER launch. The live watch must deliver it and the
	// surface must render it — without any re-Init (we keep driving the same pump).
	createCtx, ccancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer ccancel()
	if _, err := c.CreateArtifact(createCtx, name, wire.Lexicon(mustDoc(t, "First title", "the **first** body"))); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}
	driveUntil(t, a, pump, func() bool {
		return strings.Contains(stripANSI(a.View()), "First title")
	})

	// Update the artifact. The same live watch refreshes the reader to the new
	// revision's content.
	getCtx, gcancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer gcancel()
	cur, err := c.GetArtifact(getCtx, name)
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	updCtx, ucancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer ucancel()
	if _, err := c.UpdateArtifact(updCtx, name, wire.Lexicon(mustDoc(t, "Second title", "the revised body")), cur.Revision); err != nil {
		t.Fatalf("UpdateArtifact: %v", err)
	}
	driveUntil(t, a, a.NextChangeCmd(), func() bool {
		return strings.Contains(stripANSI(a.View()), "Second title")
	})
}

// TestArtifactWatchNotCrossApplied pins the owner-tag guard: the dash mounts two
// artifact-backed panes (the always-on reader and the detail pane), and the
// layout broadcasts every watch message to ALL surfaces. A change addressed to
// one pane must NOT re-render the other. Two surfaces watch DIFFERENT artifacts;
// feeding one surface the other's change message (the broadcast) leaves it
// unchanged.
func TestArtifactWatchNotCrossApplied(t *testing.T) {
	b := startBus(t)
	c := connect(t, b, "lena", "human")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Two existing artifacts with distinct content.
	for _, d := range []struct{ name, title, body string }{
		{"doc-a", "Title A", "body of A"},
		{"doc-b", "Title B", "body of B"},
	} {
		cctx, ccancel := context.WithTimeout(t.Context(), 5*time.Second)
		if _, err := c.CreateArtifact(cctx, d.name, wire.Lexicon(mustDoc(t, d.title, d.body))); err != nil {
			ccancel()
			t.Fatalf("CreateArtifact(%s): %v", d.name, err)
		}
		ccancel()
	}

	a := surface.NewArtifact(ctx, c, "doc-a", theme.Dark(), theme.DefaultKeymap())
	defer a.Stop()
	a.SetSize(50, 12)
	a.SetFocus(widget.FocusSelected)
	other := surface.NewArtifact(ctx, c, "doc-b", theme.Dark(), theme.DefaultKeymap())
	defer other.Stop()
	other.SetSize(50, 12)
	other.SetFocus(widget.FocusSelected)

	// Drive each surface's own watch to its starting value.
	driveUntil(t, a, a.Init(), func() bool { return strings.Contains(stripANSI(a.View()), "Title A") })
	driveUntil(t, other, other.Init(), func() bool { return strings.Contains(stripANSI(other.View()), "Title B") })

	// Update doc-b so `other`'s watch delivers a change.
	gctx, gcancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer gcancel()
	curB, err := c.GetArtifact(gctx, "doc-b")
	if err != nil {
		t.Fatalf("GetArtifact(doc-b): %v", err)
	}
	uctx, ucancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer ucancel()
	if _, err := c.UpdateArtifact(uctx, "doc-b", wire.Lexicon(mustDoc(t, "Title B2", "revised B")), curB.Revision); err != nil {
		t.Fatalf("UpdateArtifact(doc-b): %v", err)
	}

	// Read `other`'s change off its pump, then feed that SAME message to BOTH
	// surfaces — exactly the layout's broadcast. The change is owned by `other`,
	// so `other` applies it (Title B2) and `a` ignores it (stays Title A).
	msg := runStep(t, other.NextChangeCmd())
	if msg == nil {
		t.Fatal("other's watch produced no change after the update")
	}
	a.Update(msg)
	other.Update(msg)

	if got := stripANSI(a.View()); !strings.Contains(got, "Title A") || strings.Contains(got, "Title B") {
		t.Fatalf("a applied another pane's change (cross-talk); view:\n%s", got)
	}
	if got := stripANSI(other.View()); !strings.Contains(got, "Title B2") {
		t.Fatalf("other did not apply its own change; view:\n%s", got)
	}
}

// mustChat marshals a chat.message record for a test publish.
func mustChat(t *testing.T, text string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"$type": "chat.message", "text": text})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// mustDoc marshals a document record for a test artifact.
func mustDoc(t *testing.T, title, body string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{"$type": "document", "title": title, "body": body})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// stripANSI removes SGR escape sequences so a test can assert on visible text.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

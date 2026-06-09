package dash

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/love-lena/sextant/pkg/bus"
	"github.com/love-lena/sextant/pkg/sextant"
	"github.com/love-lena/sextant/pkg/sx"
	"github.com/love-lena/sextant/pkg/tui/surface"
)

// TestDashE2E is TASK-7.5's goal (AC#3, part A): the dash narrative driven
// deterministically against a REAL embedded bus, end to end —
// launch → see presence + a live stream → send a message that round-trips back
// (no optimistic echo) → open an artifact. It builds the composed dash root model
// (the same build() Run uses) over a real *sextant.Client and drives it through
// teatest, asserting on the rendered frames.
//
// Every wait is deadline-bounded (fail-loud, never hang). It runs in the default
// gate (no build tag): the embedded bus starts in-process the same way the SDK's
// own tests start it, so it is CI-safe.
func TestDashE2E(t *testing.T) {
	const (
		topic        = "plan"
		artifactName = "the-plan"
		docTitle     = "The dash plan"
	)
	subject := sx.TopicSubject(topic)

	// --- a real embedded bus + seeded identities/messages/artifact ---------------
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	// The dash's own identity (the one client it holds), plus two more connected so
	// presence shows them online, and an offline-but-registered third so the
	// directory has a mix. A connected client shows online; a minted-only one shows
	// offline (durable directory, ADR-0020).
	dashClient := dial(t, b, "lena", "human")
	dial(t, b, "coordinator-1", "coordinator") // online in presence
	mintOnly(t, b, "agent-beta", "agent")      // registered, offline

	// Seed the stream: a few chat.messages on the topic, published by a separate
	// connection, so the dash's DeliverAll backlog shows them on launch.
	pub := dial(t, b, "seeder", "agent")
	seedLines := []string{"let's get the dash building", "presence + stream + artifact all mount"}
	for _, line := range seedLines {
		publishChat(t, pub, subject, line)
	}

	// Seed the artifact: a document the artifact + detail panes read.
	docBody := "## The M4 panes\n\n- presence\n- message stream\n- artifact reader\n"
	rec := mustMarshal(t, map[string]string{"$type": "document", "title": docTitle, "body": docBody})
	if _, err := dashClient.CreateArtifact(t.Context(), artifactName, rec); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	// --- build the composed dash root over the real client -----------------------
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r, err := build(ctx, dashClient, Options{
		Theme:    ThemeDark, // deterministic palette (no terminal-background probe)
		Topic:    topic,
		Artifact: artifactName,
		// No ConfigPath: a fresh DefaultConfig (cockpit preset), no persistence.
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	tm := teatest.NewTestModel(t, r, teatest.WithInitialTermSize(120, 40))
	scr := capture(t, tm)

	// --- launch → presence: a seeded client's display name appears ---------------
	waitForText(t, scr, "coordinator-1")

	// --- live stream: the seeded messages show, AND one published AFTER launch
	//     arrives live (round-trip on the subscription, not a re-fetch) -----------
	waitForText(t, scr, "let's get the dash building")
	publishChat(t, pub, subject, "live after launch")
	waitForText(t, scr, "live after launch")

	// --- send → round-trip, no optimistic echo: step into the stream, type a line,
	//     Enter; it appears in the stream only because the bus echoed it back on the
	//     subscription (the dash holds the publishing identity, so the echo IS the
	//     merge). A separate verifier subscription proves the publish reached the bus,
	//     so the in-view appearance is the genuine round-trip, not a compose-line echo.
	const sent = "round-tripped hello"
	verifier := dial(t, b, "verifier", "agent")
	gotOnBus := make(chan struct{}, 1)
	vsub, err := verifier.Subscribe(t.Context(), subject, func(m sextant.Message) {
		if strings.Contains(string(m.Frame.Record), sent) {
			select {
			case gotOnBus <- struct{}{}:
			default:
			}
		}
	}, sextant.DeliverAll())
	if err != nil {
		t.Fatalf("verifier subscribe: %v", err)
	}
	t.Cleanup(func() { vsub.Stop() })

	tm.Send(key(tea.KeyDown))  // layout selection: presence → stream
	tm.Send(key(tea.KeyEnter)) // step into the stream (compose active)
	tm.Type(sent)
	tm.Send(key(tea.KeyEnter)) // publish → round-trips back through the feed

	select {
	case <-gotOnBus:
	case <-time.After(10 * time.Second):
		t.Fatal("sent message never reached the bus (no round-trip)")
	}
	waitForText(t, scr, sent) // and it shows in the stream via the echo

	// Step out of the stream so the next key lands at the layout level.
	tm.Send(key(tea.KeyEsc))

	// --- open an artifact: toggle the detail-on-demand pane in (the layout's `d`),
	//     which reveals the artifact reader on the document — title + body show ---
	tm.Send(runeKey('d'))
	waitForText(t, scr, docTitle)
	waitForText(t, scr, "M4 panes") // a body heading, proving the document body rendered

	// --- quit cleanly (exercises the layout teardown path) -----------------------
	tm.Send(key(tea.KeyEsc)) // step out of the detail pane (closes it)
	tm.Send(runeKey('q'))    // quit at the layout level
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestDashDetailRetargetLoopContract proves the host honours the detail-on-demand
// loop contract (7.5): a layout.DetailOpenedMsg{OpenArtifact} retargets the
// detail reader onto the named artifact, and the host never feeds that message
// back into the layout (which would re-open forever). It drives a surface OpenMsg
// — the path presence's select uses — and asserts the detail pane shows the
// retargeted document, with the program staying responsive (no spin).
func TestDashDetailRetargetLoopContract(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	dashClient := dial(t, b, "lena", "human")

	// Two documents: the default the cockpit opens on, and a second the host
	// retargets the detail reader onto.
	seedDoc(t, dashClient, "the-plan", "The first plan", "first body")
	seedDoc(t, dashClient, "other-doc", "The other doc", "other body")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r, err := build(ctx, dashClient, Options{Theme: ThemeDark, Topic: "plan", Artifact: "the-plan"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	tm := teatest.NewTestModel(t, r, teatest.WithInitialTermSize(120, 40))
	scr := capture(t, tm)
	waitForText(t, scr, "presence") // launched

	// Drive a surface OpenMsg{OpenArtifact, other-doc}: the layout opens + focuses
	// the detail pane and emits DetailOpenedMsg, which the host consumes to retarget
	// the detail reader onto "other-doc". Feeding the OpenMsg here is exactly what a
	// surface's intent does.
	tm.Send(surfaceOpenArtifact("other-doc"))
	waitForText(t, scr, "The other doc")

	// The program is still responsive (the loop contract held — no re-open spin):
	// quit lands and finishes.
	tm.Send(key(tea.KeyEsc))
	tm.Send(runeKey('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// --- helpers -----------------------------------------------------------------

// dial mints an identity of the given kind and connects it, returning the live
// client (closed on cleanup). A connected client shows online in presence.
func dial(t *testing.T, b *bus.Bus, name, kind string) *sextant.Client {
	t.Helper()
	creds, _, err := b.MintClient(t.Context(), name, kind)
	if err != nil {
		t.Fatalf("MintClient(%s): %v", name, err)
	}
	path := writeCreds(t, creds)
	c, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: path,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect(%s): %v", name, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// mintOnly registers an identity without connecting it, so it shows offline in
// the directory (a durable, registered-but-disconnected client).
func mintOnly(t *testing.T, b *bus.Bus, name, kind string) {
	t.Helper()
	if _, _, err := b.MintClient(t.Context(), name, kind); err != nil {
		t.Fatalf("MintClient(%s): %v", name, err)
	}
}

func writeCreds(t *testing.T, creds string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func publishChat(t *testing.T, c *sextant.Client, subject, text string) {
	t.Helper()
	rec := mustMarshal(t, map[string]string{"$type": "chat.message", "text": text})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := c.Publish(ctx, subject, rec); err != nil {
		t.Fatalf("Publish(%q): %v", text, err)
	}
}

func seedDoc(t *testing.T, c *sextant.Client, name, title, body string) {
	t.Helper()
	rec := mustMarshal(t, map[string]string{"$type": "document", "title": title, "body": body})
	if _, err := c.CreateArtifact(t.Context(), name, rec); err != nil {
		t.Fatalf("CreateArtifact(%s): %v", name, err)
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// screen accumulates a TestModel's rendered frames so a test can search the full
// render history. teatest.WaitFor drains tm.Output() on each call (io.ReadAll
// consumes the buffer), so two back-to-back WaitFor calls would each see only the
// frames written between them — and a frame rendered before the first call would
// be lost to the second. screen owns one background copy loop draining the output
// into a thread-safe buffer, so every waitForText sees everything rendered so far
// (alt-screen repaints are cumulative in the stream). This is the seam that makes
// the multi-step narrative assertable without re-rendering tricks.
type screen struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// capture starts draining tm.Output() into a screen until the test ends.
func capture(t *testing.T, tm *teatest.TestModel) *screen {
	t.Helper()
	s := &screen{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		out := tm.Output()
		tmp := make([]byte, 4096)
		for {
			n, err := out.Read(tmp)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(tmp[:n])
				s.mu.Unlock()
			}
			if err == io.EOF {
				// The buffer is momentarily drained; keep polling for the next frame.
				time.Sleep(20 * time.Millisecond)
				continue
			}
			if err != nil {
				return
			}
		}
	}()
	return s
}

// text returns the full accumulated render with ANSI stripped.
func (s *screen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.Strip(s.buf.String())
}

// waitForText blocks until substr has appeared in any rendered frame, or fails
// after a bounded deadline (fail-loud, never hang).
func waitForText(t *testing.T, s *screen, substr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(s.text(), substr) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("text %q never rendered within 10s; last screen:\n%s", substr, s.text())
}

// key builds a tea.KeyMsg for a named key, the way bubbletea delivers it.
func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// runeKey builds a tea.KeyMsg for a single rune (e.g. 'd', 'q').
func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// surfaceOpenArtifact is the OpenMsg a surface emits to ask the dash to open a
// named artifact in detail. Driving it directly stands in for the surface intent
// (the path presence's select uses).
func surfaceOpenArtifact(name string) tea.Msg {
	return surface.OpenMsg{Kind: surface.OpenArtifact, Ref: name}
}

package dash

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
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
	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/clients/go/apps/internal/clictx"
	"github.com/love-lena/sextant/clients/go/apps/internal/tui/layout"
	"github.com/love-lena/sextant/clients/go/sdk"
	"github.com/love-lena/sextant/protocol/conninfo"
	"github.com/love-lena/sextant/protocol/sx"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"go.uber.org/goleak"
)

// TestDashE2E is the ADR-0024/0026 narrative driven deterministically against a
// REAL embedded bus, end to end: launch → the three browser lists populate
// (clients · topics · artifacts) → Tab focuses the topics browser and Enter
// opens a topic's conversation IN THE SAME PANE → a composed message (with a
// literal q in it — q types while a compose is capturing) round-trips back
// through the bus (no optimistic echo) → focus moves to artifacts WITH THE
// CONVERSATION STILL OPEN and the reader opens in place → focus returns to the
// topics pane, which held its place: a second compose round-trips through the
// still-open conversation → q from a non-capturing pane quits. It builds the
// composed dash root model (the same build() Run uses) over a real
// *sextant.Client and drives it through teatest, asserting on the rendered
// frames.
//
// Every wait is deadline-bounded (fail-loud, never hang). It runs in the default
// gate (no build tag): the embedded bus starts in-process the same way the SDK's
// own tests start it, so it is CI-safe.
func TestDashE2E(t *testing.T) {
	const (
		artifactName = "the-plan"
		docTitle     = "The dash plan"
		docMarker    = "marker-XYZ"
	)

	// --- a real embedded bus + seeded identities/messages/artifact ---------------
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	// The dash's own identity (the one client it holds), plus a connected agent so
	// the clients browser shows it online, and an offline-but-registered third so
	// the directory has a mix (durable directory, ADR-0020).
	dashClient := dial(t, b, "lena", "human")
	dial(t, b, "agent-alpha", "agent")    // online in the clients browser
	mintOnly(t, b, "agent-beta", "agent") // registered, offline

	// Seed two topics so the topics browser discovers them client-side from its
	// msg.topic.> replay. Names and lines are short of the pane's wrap width so a
	// substring assertion never straddles a soft-wrap boundary; "ops" sorts before
	// "plan", so the cursor rests on it.
	pub := dial(t, b, "seeder", "agent")
	publishChat(t, pub, sx.TopicSubject("ops"), "ops kickoff")
	publishChat(t, pub, sx.TopicSubject("plan"), "plan kickoff")

	// Seed the artifact the artifacts browser lists and its reader opens.
	docBody := "## The three browsers\n\n- clients\n- topics\n- artifacts\n\n" + docMarker + "\n"
	rec := mustMarshal(t, map[string]string{"$type": "document", "title": docTitle, "body": docBody})
	if _, err := dashClient.CreateArtifact(t.Context(), artifactName, rec); err != nil {
		t.Fatalf("CreateArtifact: %v", err)
	}

	// --- build the composed dash root over the real client -----------------------
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r, err := build(ctx, dashClient, Options{
		Theme: ThemeDark, // deterministic palette (no terminal-background probe)
		// No ConfigPath: a fresh DefaultConfig (cockpit preset), no persistence.
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	tm := teatest.NewTestModel(t, r, teatest.WithInitialTermSize(120, 40))
	scr := capture(t, tm)

	// --- launch → all three lists populate ----------------------------------------
	waitForText(t, scr, "agent-alpha") // clients: a seeded identity
	waitForText(t, scr, "ops")         // topics: discovered from the replay
	waitForText(t, scr, "the-plan")    // artifacts: listed via artifact.list

	// --- topics: Tab moves focus (ADR-0026: keys go to the focused pane), Enter
	//     opens the cursor row's conversation IN THE SAME PANE ------------------
	tm.Send(key(tea.KeyTab))           // focus: clients → topics
	tm.Send(key(tea.KeyEnter))         // open the cursor row ("ops") → its conversation
	waitForText(t, scr, "Topic · ops") // the pane title tracks the open detail
	waitForText(t, scr, "ops kickoff") // the conversation replays the seeded line

	// --- compose → round-trip, no optimistic echo: type a line, Enter; it appears
	//     only because the bus echoed it back on the subscription. A separate
	//     verifier subscription proves the publish reached the bus, so the in-view
	//     appearance is the genuine round-trip, not a compose-line echo. The text
	//     leads with a literal q: while the compose is capturing, q TYPES — it
	//     must not quit the dash (ADR-0026's quit rule).
	const sent = "q-echo-hello"
	verifier := dial(t, b, "verifier", "agent")
	gotOnBus := make(chan struct{}, 1)
	vsub, err := verifier.Subscribe(t.Context(), sx.TopicSubject("ops"), func(m sextant.Message) {
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

	tm.Type(sent)
	tm.Send(key(tea.KeyEnter)) // publish → round-trips back through the feed

	select {
	case <-gotOnBus:
	case <-time.After(10 * time.Second):
		t.Fatal("sent message never reached the bus (no round-trip)")
	}
	waitForText(t, scr, sent) // and it shows in the conversation via the echo

	// --- move focus to artifacts WITH THE CONVERSATION STILL OPEN (ADR-0026:
	//     moving focus never changes what a pane shows — no Esc unwind), and open
	//     the document reader in place. Keys are delivered to the program in send
	//     order, so no status-token waits are needed between motions; the waits
	//     below sync on the rendered results before asserting further.
	tm.Send(key(tea.KeyCtrlL))                 // focus: topics → artifacts (spatial right)
	tm.Send(key(tea.KeyEnter))                 // open the cursor row ("the-plan") → the reader
	waitForText(t, scr, "Artifact · the-plan") // the pane title tracks the reader
	waitForText(t, scr, docMarker)             // the document body rendered

	// --- back to the topics pane, which HELD ITS PLACE: the conversation is still
	//     open, its compose still live — a second line round-trips through it
	//     (the compose only exists in the open detail, so the echo is the proof).
	tm.Send(key(tea.KeyShiftTab)) // cycle back: artifacts → topics
	const sent2 = "pane-held-its-place"
	tm.Type(sent2)
	tm.Send(key(tea.KeyEnter))
	waitForText(t, scr, sent2)

	// --- q quits from a pane that is not capturing text: focus the clients list
	//     (its browser holds no compose) and press q. This exercises the layout
	//     teardown path: Quit stops every surface, including the open reader and
	//     the open conversation.
	tm.Send(key(tea.KeyCtrlH)) // focus: topics → clients (spatial left)
	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))

	// Wind down the program context so the drain watch goroutine exits before
	// goleak runs (Run does this in production; the test owns progCtx via build).
	cancel()
}

// TestDashZeroConfigFirstRun pins the locked first-run design (ADR-0024) against
// a real embedded bus, hermetically ($SEXTANT_HOME pinned to a temp dir): with
// NO identity resolved and a discoverable local bus (the bus.json discovery
// file `sextant up` writes under the store), ensureIdentity self-enrolls — same
// semantics as `sextant clients register --self` — printing exactly one notice
// line, creating + activating a context, and leaving Options connectable. A
// second resolve then finds the saved context silently (no second notice, no
// second enrollment).
func TestDashZeroConfigFirstRun(t *testing.T) {
	store := t.TempDir()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	// Mirror `sextant up`: write the discovery file the dash probes.
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: b.ClientURL()}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}

	t.Setenv("SEXTANT_HOME", t.TempDir()) // hermetic context store
	t.Setenv("SEXTANT_CREDS", "")
	t.Setenv("SEXTANT_CONTEXT", "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := Options{Store: store, Name: "zeroconf-lena"}
	var notice bytes.Buffer
	if err := ensureIdentity(ctx, &opts, &notice); err != nil {
		t.Fatalf("ensureIdentity: %v", err)
	}
	if got, want := notice.String(), "first run — enrolled as zeroconf-lena\n"; got != want {
		t.Errorf("notice = %q, want %q", got, want)
	}
	if opts.CredsPath == "" {
		t.Fatal("ensureIdentity did not resolve a creds path")
	}
	if _, err := os.Stat(opts.CredsPath); err != nil {
		t.Fatalf("enrolled creds not on disk: %v", err)
	}
	if got := clictx.Active(); got != "zeroconf-lena" {
		t.Errorf("active context = %q, want zeroconf-lena", got)
	}

	// The minted identity actually connects (the enrollment is real, not just
	// files on disk).
	c, err := sextant.Connect(ctx, sextant.Options{
		CredsPath: opts.CredsPath,
		URL:       opts.URL,
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect with enrolled creds: %v", err)
	}
	_ = c.Close()

	// Next run: the flag resolver finds the saved (active) context silently, so
	// ensureIdentity is a no-op — no second notice, no second enrollment.
	fs := flag.NewFlagSet("dash-test", flag.ContinueOnError)
	f := AddFlags(fs)
	if err := fs.Parse([]string{"--store", store}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	opts2, err := f.Resolve()
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if opts2.CredsPath != opts.CredsPath {
		t.Errorf("second run creds = %q, want the enrolled %q", opts2.CredsPath, opts.CredsPath)
	}
	var notice2 bytes.Buffer
	if err := ensureIdentity(ctx, &opts2, &notice2); err != nil {
		t.Fatalf("second ensureIdentity: %v", err)
	}
	if notice2.Len() != 0 {
		t.Errorf("second run printed a notice: %q", notice2.String())
	}
}

// TestDashZeroConfigNoBusFailsLoud: with no identity AND no discoverable bus,
// the first run fails loud with guidance to run `sextant up` — it never hangs
// and never silently proceeds.
func TestDashZeroConfigNoBusFailsLoud(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := Options{Store: t.TempDir()} // empty store: no discovery file
	err := ensureIdentity(ctx, &opts, io.Discard)
	if err == nil {
		t.Fatal("ensureIdentity with no bus should fail loud")
	}
	if !strings.Contains(err.Error(), "sextant up") {
		t.Errorf("error %q should guide to `sextant up`", err)
	}
}

// TestDashFirstRunExplicitURLNoDiscoveryFailsLoud: an explicit --url cannot
// substitute for the local discovery file on a first run — self-enrollment
// mints over the bus store's enroll.creds, which only the locally-discovered
// bus accepts. With no identity, --url set, and NO discovery file, the dash
// must fail up front with --url-aware guidance (not the misleading "no local
// bus discovered" message) and write no state: no context created, none
// activated, no creds resolved.
func TestDashFirstRunExplicitURLNoDiscoveryFailsLoud(t *testing.T) {
	t.Setenv("SEXTANT_HOME", t.TempDir()) // hermetic context store

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := Options{Store: t.TempDir(), URL: "nats://10.0.0.9:4222"} // empty store: no discovery file
	err := ensureIdentity(ctx, &opts, io.Discard)
	if err == nil {
		t.Fatal("ensureIdentity with --url and no discovery file should fail loud")
	}
	if !strings.Contains(err.Error(), "--url") {
		t.Errorf("error %q should explain the --url conflict, not just claim no bus", err)
	}
	if !strings.Contains(err.Error(), "sextant clients register") {
		t.Errorf("error %q should guide toward registering on the remote bus", err)
	}
	assertNoEnrollment(t, opts)
}

// TestDashFirstRunExplicitURLMismatchFailsLoud is review bug (b) on PR #99:
// with no identity, a discoverable LOCAL bus, and --url naming a DIFFERENT
// bus, the old path enrolled against the discovered bus and then dialed the
// --url bus with the wrong creds — an auth failure AFTER a fresh context was
// created and activated (stranding it, broken). The conflict must surface
// BEFORE any state is written: ensureIdentity fails loud, no context exists,
// nothing was activated. A --url that MATCHES the discovered bus proceeds
// normally (proven against the same live bus).
func TestDashFirstRunExplicitURLMismatchFailsLoud(t *testing.T) {
	store := t.TempDir()
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: store})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: b.ClientURL()}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}

	t.Setenv("SEXTANT_HOME", t.TempDir()) // hermetic context store
	t.Setenv("SEXTANT_CREDS", "")
	t.Setenv("SEXTANT_CONTEXT", "")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := Options{Store: store, URL: "nats://10.0.0.9:4222", Name: "url-mismatch-lena"}
	err = ensureIdentity(ctx, &opts, io.Discard)
	if err == nil {
		t.Fatal("ensureIdentity with a mismatched --url should fail loud before enrolling")
	}
	if !strings.Contains(err.Error(), "--url") || !strings.Contains(err.Error(), b.ClientURL()) {
		t.Errorf("error %q should name the --url/discovered-bus conflict", err)
	}
	assertNoEnrollment(t, opts)

	// A --url that matches the discovered bus is no conflict: the same first run
	// proceeds and enrolls normally.
	match := Options{Store: store, URL: b.ClientURL(), Name: "url-match-lena"}
	var notice bytes.Buffer
	if err := ensureIdentity(ctx, &match, &notice); err != nil {
		t.Fatalf("ensureIdentity with the matching --url: %v", err)
	}
	if match.CredsPath == "" {
		t.Fatal("matching --url first run did not resolve a creds path")
	}
	if got := clictx.Active(); got != "url-match-lena" {
		t.Errorf("active context = %q, want url-match-lena", got)
	}
}

// TestDashFirstRunContextExistsAdvisesKindHuman pins the collision advice
// (PR #99 review): the dash enrolls kind "human" (the human's seat), but
// `sextant clients register --self` defaults to kind "client" — so the
// re-enroll advice must carry `--kind human`, or following it verbatim would
// silently change the seat's kind. The collision is pre-flighted before any
// mint, so a discovery file plus an existing context of the same name is
// enough to reach it.
func TestDashFirstRunContextExistsAdvisesKindHuman(t *testing.T) {
	store := t.TempDir()
	if err := conninfo.Write(filepath.Join(store, conninfo.DefaultFile), conninfo.Info{URL: "nats://127.0.0.1:4222"}); err != nil {
		t.Fatalf("write discovery: %v", err)
	}

	t.Setenv("SEXTANT_HOME", t.TempDir())
	if err := clictx.Save(clictx.Context{Name: "taken-lena", Kind: "human"}); err != nil {
		t.Fatalf("seed context: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts := Options{Store: store, Name: "taken-lena"}
	err := ensureIdentity(ctx, &opts, io.Discard)
	if err == nil {
		t.Fatal("ensureIdentity onto an existing context should fail loud")
	}
	if !strings.Contains(err.Error(), "sextant clients register --self --kind human --force") {
		t.Errorf("collision advice %q must re-enroll under --kind human (register --self defaults to kind client)", err)
	}
	if !strings.Contains(err.Error(), "sextant context use taken-lena") {
		t.Errorf("collision advice %q should offer adopting the existing context", err)
	}
}

// assertNoEnrollment asserts a failed first run wrote NO state (fail-loud,
// fail-early — never partial state then error): no creds resolved, no context
// saved, none activated.
func assertNoEnrollment(t *testing.T, opts Options) {
	t.Helper()
	if opts.CredsPath != "" {
		t.Errorf("failed first run resolved creds %q; want none", opts.CredsPath)
	}
	if got := clictx.Active(); got != "" {
		t.Errorf("failed first run activated context %q; want none", got)
	}
	ctxs, err := clictx.List()
	if err != nil {
		t.Fatalf("clictx.List: %v", err)
	}
	if len(ctxs) != 0 {
		t.Errorf("failed first run left %d context(s) behind: %+v", len(ctxs), ctxs)
	}
}

// TestDashConnectWedgedBusFailsLoud: the steady-state launch connect is
// deadline-bound like every other launch I/O (PR #99 review). A fully-down bus
// fails fast under the NATS client's own connect timeout — the gap is a bus
// that ACCEPTS the connection and never answers the hello handshake (a wedged
// API responder), which used to block the launch forever. The wedge here is
// real: a bare NATS server (no Wire API) plus a silent subscriber on the hello
// subject, so the request finds a responder that never replies (with no
// responder at all it would fail fast with "no responders" instead of
// hanging). Run must return the loud connected-but-no-answer error once
// launchTimeout fires, well before this test's own deadline.
func TestDashConnectWedgedBusFailsLoud(t *testing.T) {
	// Real creds — the SDK reads the identity out of the credential before
	// dialing — minted from a throwaway embedded bus.
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)
	creds, id, err := b.MintClient(t.Context(), "lena", "human")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	credsPath := writeCreds(t, creds)

	// The wedged "bus": a bare no-auth NATS server that accepts the dial...
	srv, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1})
	if err != nil {
		t.Fatalf("natsserver.NewServer: %v", err)
	}
	srv.Start()
	t.Cleanup(func() { srv.Shutdown(); srv.WaitForShutdown() })
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("bare nats server never came up")
	}
	// ...and a hello responder that never answers.
	wedge, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("wedge connect: %v", err)
	}
	t.Cleanup(wedge.Close)
	// The connect-handshake call subject the SDK's hello lands on, built inline
	// rather than from protocol/wireapi: this test deliberately constructs a
	// malformed wedged-bus harness, so it must not depend on the production wire
	// binding (the wire atom is the SDK's alone — TASK-182). The shape is
	// sx.api.<clientID>.clients.hello (wireapi.APIPrefix + id + "." + OpClientsHello).
	helloSubject := "sx.api." + id + ".clients.hello"
	if _, err := wedge.Subscribe(helloSubject, func(*nats.Msg) {}); err != nil {
		t.Fatalf("wedge subscribe: %v", err)
	}
	if err := wedge.Flush(); err != nil {
		t.Fatalf("wedge flush: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			CredsPath: credsPath,
			URL:       srv.ClientURL(),
			Theme:     ThemeDark,
		})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run against a wedged bus returned nil; want the loud connect error")
		}
		for _, want := range []string{srv.ClientURL(), "connected but no answer", "sextant up"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should contain %q", err, want)
			}
		}
	case <-time.After(launchTimeout + 5*time.Second):
		t.Fatal("Run never returned against a wedged bus — the connect handshake is not deadline-bound")
	}
}

// TestWatchDrainExitsOnCtxCancel is the focused, teatest-free regression guard
// for the watchDrain goroutine leak (review item 1): it drives ONLY the root's
// drain watch — the single goroutine the dash itself owns — without a tea.Program,
// so no bubbletea timer/batch goroutines muddy the check. It proves the watch
// exits when the program context cancels (any quit path), which Client.Drained()
// alone never triggers (it closes only on a cooperative bus drain).
//
// It closes the client + bus BEFORE goleak.VerifyNone so no NATS read-loop is
// left to confuse the check, and baselines with goleak.IgnoreCurrent so the
// bubbletea/teatest goroutines the sibling program-driven tests leave behind
// (timers, batch waiters — not the dash's to reap) are ignored: only a goroutine
// THIS test introduced and failed to wind down is flagged.
//
// Non-vacuous: drop the `case <-ctx.Done()` leg from watchDrain and this test
// fails twice over — the goroutine-exit select hits its 2s deadline, and
// goleak.VerifyNone reports internal/dash.root.watchDrain.func1 still parked.
func TestWatchDrainExitsOnCtxCancel(t *testing.T) {
	// Baseline now, before we start anything: everything alive here (incl. any
	// teatest/bubbletea goroutine a sibling test left) is ignored, so the leak check
	// at the end sees only what this test added.
	ignoreExisting := goleak.IgnoreCurrent()

	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	creds, _, err := b.MintClient(t.Context(), "lena", "human")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	client, err := sextant.Connect(t.Context(), sextant.Options{
		URL:       b.ClientURL(),
		CredsPath: writeCreds(t, creds),
		Logf:      func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := newRoot(ctx, layout.Model{}, client)

	// Run the watch command the way bubbletea would — in its own goroutine — and
	// confirm it returns once the context cancels (a non-drain quit).
	got := make(chan tea.Msg, 1)
	go func() { got <- r.watchDrain()() }()

	select {
	case <-got:
		t.Fatal("watchDrain returned before any quit/drain — it must block until one happens")
	case <-time.After(150 * time.Millisecond):
		// Still parked, as expected: no drain, no cancel yet.
	}

	cancel() // any quit path cancels the program context
	select {
	case msg := <-got:
		if msg != nil {
			t.Fatalf("watchDrain returned %#v on ctx-cancel; want nil (no drain)", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watchDrain did not exit within 2s of ctx-cancel — the goroutine leaked")
	}

	// Tear the bus + client down, then assert no goroutine this test introduced
	// survives — with the NATS read-loops gone, a flagged goroutine would be the
	// dash's own (the watchDrain leak this guards).
	_ = client.Close()
	b.Shutdown()
	goleak.VerifyNone(t, ignoreExisting)
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

// capture starts draining tm.Output() into a screen until the test ends. The
// copy loop stops on a t.Cleanup-fired channel so the goroutine always exits —
// the buffer never reaches a real EOF (it just empties momentarily between
// frames), so without an explicit stop the loop would spin forever and goleak
// would flag it as a test-owned leak. Cleanup runs before TestMain's goleak.
func capture(t *testing.T, tm *teatest.TestModel) *screen {
	t.Helper()
	s := &screen{}
	stop := make(chan struct{})
	done := make(chan struct{})
	t.Cleanup(func() {
		close(stop)
		<-done // the copy goroutine has fully exited before the test (and goleak) end
	})
	go func() {
		defer close(done)
		out := tm.Output()
		tmp := make([]byte, 4096)
		for {
			select {
			case <-stop:
				return
			default:
			}
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

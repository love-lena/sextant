package dashserve

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
)

// permissionsViolation is the NATS async-error substring the standing dash
// connection's `sx.hb` subscribe raised in TASK-185. With no standing connection
// (ADR-0046, TASK-187) it must never appear — at rest there is no subscription to
// be denied.
const permissionsViolation = "Permissions Violation"

// TestNoStandingConnectionAtRest is AC#1 + AC#3 for the serve path: with Run up
// and NO browser tab open, the dash holds no bus connection, so the per-request
// minter is never asked to connect, and the `sx.hb` permissions violation that
// the old standing connection raised on startup (TASK-185) cannot occur — there
// is no subscription to be denied. It drives the real serve path against an
// embedded bus, instruments the minter's connect step, and asserts zero connects
// at rest plus no violation in the captured connection log.
func TestNoStandingConnectionAtRest(t *testing.T) {
	store := t.TempDir()
	wsAddr := freeLoopbackAddr(t)
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: store, WebSocketListenAddr: wsAddr})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	creds, _, err := b.MintClient(t.Context(), "dash", "human")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	credsPath := writeCreds(t, creds)
	writeConnInfo(t, store, b.ClientURL(), "ws://"+wsAddr)

	var (
		mu       sync.Mutex
		connects int
	)
	log := &syncBuffer{}
	// Instrument the minter Run builds: wrap the real connect to count it, and
	// route the connection's SDK announce log into a buffer so any startup chatter
	// (a stray connection's violation) is observable. A connection at rest would
	// both bump the counter and surface in the log.
	restore := setNewMinter(func(credsPath, url, connInfoPath string) *connectMinter {
		m := newConnectMinter(credsPath, url, connInfoPath)
		m.logf = func(format string, args ...any) { log.logf(format, args...) }
		real := m.realConnect
		m.connect = func(ctx context.Context) (sessionClient, error) {
			mu.Lock()
			connects++
			mu.Unlock()
			return real(ctx)
		}
		return m
	})
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{CredsPath: credsPath, URL: b.ClientURL(), Store: store, Port: 0}, out)
	}()

	waitForServeURL(t, out)
	time.Sleep(200 * time.Millisecond) // let any rogue startup connection land — there should be none

	mu.Lock()
	n := connects
	mu.Unlock()
	if n != 0 {
		t.Fatalf("serve opened %d bus connections at rest (no tab open), want 0 — the dash must hold no standing connection", n)
	}
	if got := log.String(); strings.Contains(got, permissionsViolation) {
		t.Fatalf("a permissions violation was raised at rest:\n%s", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of context cancel")
	}
}

// TestMintConnectsOnceNoViolation is AC#2 + AC#3 against a real bus: the first
// POST /api/session connects exactly once, mints a working session credential,
// and closes the connection before the request returns — and that real
// clients.session round-trip raises no `sx.hb` permissions violation. It drives
// the serve path against an embedded bus with the WebSocket listener on,
// instruments the minter's connect/close, and asserts the counts and the clean
// connection log.
func TestMintConnectsOnceNoViolation(t *testing.T) {
	store := t.TempDir()
	wsAddr := freeLoopbackAddr(t)
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: store, WebSocketListenAddr: wsAddr})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	creds, _, err := b.MintClient(t.Context(), "dash", "human")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	credsPath := writeCreds(t, creds)
	wsURL := "ws://" + wsAddr
	writeConnInfo(t, store, b.ClientURL(), wsURL)

	var (
		mu               sync.Mutex
		connects, closes int
	)
	log := &syncBuffer{}
	restore := setNewMinter(func(credsPath, url, connInfoPath string) *connectMinter {
		m := newConnectMinter(credsPath, url, connInfoPath)
		m.logf = func(format string, args ...any) { log.logf(format, args...) }
		real := m.realConnect
		m.connect = func(ctx context.Context) (sessionClient, error) {
			mu.Lock()
			connects++
			mu.Unlock()
			c, err := real(ctx)
			if err != nil {
				return nil, err
			}
			return &closeCountingClient{sessionClient: c, onClose: func() {
				mu.Lock()
				closes++
				mu.Unlock()
			}}, nil
		}
		return m
	})
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{CredsPath: credsPath, URL: b.ClientURL(), Store: store, Port: 0}, out)
	}()

	base, token := waitForServeURL(t, out)

	var sess struct {
		ID    string `json:"id"`
		Creds string `json:"creds"`
		WSURL string `json:"wsURL"`
	}
	postJSON(t, base+"/api/session", token, &sess)
	if sess.Creds == "" || sess.WSURL != wsURL {
		t.Fatalf("session response not usable: %+v", sess)
	}

	mu.Lock()
	gotConnects, gotCloses := connects, closes
	mu.Unlock()
	if gotConnects != 1 {
		t.Fatalf("one POST /api/session opened %d connections, want exactly 1", gotConnects)
	}
	// Close runs in the handler's MintSession via defer — done before the response
	// was written, so by the time postJSON returned the connection is gone.
	if gotCloses != 1 {
		t.Fatalf("connection closed %d times after the request returned, want 1 (closed within the request)", gotCloses)
	}
	if got := log.String(); strings.Contains(got, permissionsViolation) {
		t.Fatalf("the mint connection raised a permissions violation:\n%s", got)
	}

	cancel()
	<-done
}

// closeCountingClient wraps a real sessionClient to observe Close, so the serve
// test can assert the per-request connection was closed.
type closeCountingClient struct {
	sessionClient
	onClose func()
}

func (c *closeCountingClient) Close() error {
	c.onClose()
	return c.sessionClient.Close()
}

// setNewMinter swaps the package's minter constructor for the duration of a test,
// returning a restore func to defer. It lets a test instrument the minter Run
// builds without exporting a hook into production code.
func setNewMinter(fn func(credsPath, url, connInfoPath string) *connectMinter) (restore func()) {
	prev := newMinter
	newMinter = fn
	return func() { newMinter = prev }
}

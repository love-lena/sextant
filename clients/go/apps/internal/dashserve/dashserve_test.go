package dashserve

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/love-lena/sextant/bus"
	"github.com/love-lena/sextant/protocol/conninfo"
)

// TestRunMintsBrowserSession is the serve-path integration (ADR-0044): Run
// connects under a real identity, binds a local listener, prints a URL carrying
// the per-launch token, and serves the shrunk surface — a static SPA host plus
// the credential-mint endpoint. POST /api/session mints a short-lived browser
// credential and hands back the ws URL the page dials, and the server shuts down
// cleanly on context cancel. It drives the whole web-dash serve glue against an
// embedded bus with the WebSocket listener on (CI-safe, default gate).
func TestRunMintsBrowserSession(t *testing.T) {
	store := t.TempDir()
	// A free loopback port for the WebSocket listener (the listener requires a
	// positive, loopback port — fail-loud on :0, like the leaf listener). Probe one
	// and release it; the bus binds it next.
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

	// The dash discovers the ws URL from the discovery file the bus writes; Run
	// reads it via resolveWSURL(opts.Store). The embedded bus.Start does not write
	// conninfo (the CLI's cmdUp does), so write it here with the ws URL the listener
	// carries — the test stands in for the cmdUp glue.
	wsURL := "ws://" + wsAddr
	writeConnInfo(t, store, b.ClientURL(), wsURL)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			CredsPath: credsPath,
			URL:       b.ClientURL(),
			Store:     store,
			Port:      0, // ephemeral port — no conflicts in tests
		}, out)
	}()

	base, token := waitForServeURL(t, out)

	// POST /api/session mints a fresh browser credential and returns it + the ws URL
	// the page dials. The minted creds are non-empty bus auth material.
	var sess struct {
		ID    string `json:"id"`
		Creds string `json:"creds"`
		WSURL string `json:"wsURL"`
	}
	postJSON(t, base+"/api/session", token, &sess)
	if sess.ID == "" || sess.Creds == "" {
		t.Fatalf("session response missing minted id/creds: %+v", sess)
	}
	if sess.WSURL != wsURL {
		t.Fatalf("session wsURL = %q, want the bus ws URL %q", sess.WSURL, wsURL)
	}

	// The bound listener is loopback-only.
	if !regexp.MustCompile(`^http://127\.0\.0\.1:`).MatchString(base) {
		t.Fatalf("served on %q, want a 127.0.0.1 address", base)
	}

	// Cancelling the context shuts the server down (no hang).
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

// TestServeAddrIsLoopback: the web dash server is local-only (ADR-0032). Only
// the port is configurable; the host is always 127.0.0.1, so a routable bind
// (e.g. 0.0.0.0) can never be expressed — the foot-gun isn't reachable by
// design.
func TestServeAddrIsLoopback(t *testing.T) {
	cases := []struct {
		port int
		want string
	}{
		{defaultServePort, "127.0.0.1:8765"},
		{0, "127.0.0.1:0"},
		{9000, "127.0.0.1:9000"},
	}
	for _, tc := range cases {
		if got := serveAddr(tc.port); got != tc.want {
			t.Fatalf("serveAddr(%d) = %q, want %q", tc.port, got, tc.want)
		}
	}
}

// waitForServeURL polls out until Run has printed its URL line, returning the
// base (scheme://host:port) and the token query value.
func waitForServeURL(t *testing.T, out *syncBuffer) (base, token string) {
	t.Helper()
	re := regexp.MustCompile(`(http://[^/\s]+)/\?token=(\S+)`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := re.FindStringSubmatch(out.String()); m != nil {
			return m[1], m[2]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Run never printed a URL line; output:\n%s", out.String())
	return "", ""
}

func postJSON(t *testing.T, url, token string, v any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s: status %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

// writeConnInfo writes the discovery file the dash reads to resolve the bus + ws
// URLs, standing in for the CLI's cmdUp glue in an embedded-bus test.
func writeConnInfo(t *testing.T, store, url, wsURL string) {
	t.Helper()
	if err := conninfo.Write(connInfoPath(store), conninfo.Info{URL: url, WSURL: wsURL}); err != nil {
		t.Fatalf("write conninfo: %v", err)
	}
}

// freeLoopbackAddr probes a free loopback host:port and releases it, for the bus
// WebSocket listener (which requires a positive loopback port — it fails loud on
// :0, like the leaf listener). There is a small race before the bus rebinds it,
// acceptable in a single-process test.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe a free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// writeCreds writes a credentials blob to a temp file and returns its path.
func writeCreds(t *testing.T, creds string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "client.creds")
	if err := os.WriteFile(path, []byte(creds), 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	return path
}

// TestRunStateFile asserts that when StateFile is given, Run writes a valid JSON
// state file (correct url, token, port; 0600 permissions) on start and removes
// it on clean shutdown.
func TestRunStateFile(t *testing.T) {
	b, err := bus.Start(t.Context(), bus.Config{StoreDir: t.TempDir()})
	if err != nil {
		t.Fatalf("bus.Start: %v", err)
	}
	t.Cleanup(b.Shutdown)

	creds, _, err := b.MintClient(t.Context(), "dash", "human")
	if err != nil {
		t.Fatalf("MintClient: %v", err)
	}
	credsPath := writeCreds(t, creds)
	stateFile := filepath.Join(t.TempDir(), "dash.json")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			CredsPath: credsPath,
			URL:       b.ClientURL(),
			Port:      0,
			StateFile: stateFile,
		}, out)
	}()

	// Wait for the server to print its URL (it writes the state file before then).
	waitForServeURL(t, out)

	// State file must exist with 0600 permissions.
	info, err := os.Stat(stateFile)
	if err != nil {
		t.Fatalf("state file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("state file permissions = %04o, want 0600", perm)
	}

	// Content must parse and match the announced URL.
	state, err := ReadStateFile(stateFile)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	re := regexp.MustCompile(`(http://[^/\s]+)/\?token=(\S+)`)
	m := re.FindStringSubmatch(out.String())
	if m == nil {
		t.Fatal("could not extract URL from output")
	}
	wantURL := m[1] + "/?token=" + m[2]
	if state.URL != wantURL {
		t.Fatalf("state.URL = %q, want %q", state.URL, wantURL)
	}
	if state.Token == "" {
		t.Fatal("state.Token is empty")
	}
	if state.Port <= 0 {
		t.Fatalf("state.Port = %d, want > 0", state.Port)
	}

	// On clean shutdown the state file must be removed.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return within 5s of context cancel")
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("state file still exists after shutdown (err=%v)", err)
	}
}

// TestReadStateFileAbsent: ReadStateFile on a missing file returns an error
// wrapping os.ErrNotExist so callers can distinguish "not running" from
// a parse failure.
func TestReadStateFileAbsent(t *testing.T) {
	_, err := ReadStateFile(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err == nil {
		t.Fatal("ReadStateFile on absent file returned nil error")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

// TestReadStateFileFixture: ReadStateFile parses a hand-written fixture so the
// URL command works against any conformant file, not just the one Run wrote.
func TestReadStateFileFixture(t *testing.T) {
	fixture := `{"url":"http://127.0.0.1:8765/?token=abc123","token":"abc123","port":8765}`
	path := filepath.Join(t.TempDir(), "dash.json")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := ReadStateFile(path)
	if err != nil {
		t.Fatalf("ReadStateFile: %v", err)
	}
	if state.URL != "http://127.0.0.1:8765/?token=abc123" {
		t.Fatalf("URL = %q", state.URL)
	}
	if state.Token != "abc123" {
		t.Fatalf("Token = %q", state.Token)
	}
	if state.Port != 8765 {
		t.Fatalf("Port = %d", state.Port)
	}
}

// syncBuffer is a goroutine-safe bytes.Buffer: Run writes its announce line from
// its own goroutine while the test polls it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

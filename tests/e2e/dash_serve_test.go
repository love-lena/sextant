//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// buildDashBinary builds the standalone sextant-dash binary (ADR-0046: the web
// dash is its own binary; `sextant dash` no longer serves). The e2e drives the
// real binary, like buildBinary does for sextant.
func buildDashBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "sextant-dash")
	cmd := exec.Command("go", "build", "-o", bin, "./clients/go/apps/dash")
	cmd.Dir = "../.." // repo root, relative to tests/e2e
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build sextant-dash: %v\n%s", err, out)
	}
	return bin
}

// TestDashServeMintsBrowserSession is the AC#3/AC#5 acceptance for the direct-WS
// dash (ADR-0044) driven through the built binary: with the bus WebSocket listener
// on, the standalone `sextant-dash` (ADR-0046) is a static-SPA host plus a
// credential-mint endpoint. POST /api/session mints a short-lived, scoped browser credential and
// hands back the ws URL the page dials; the old /api/* relay (self/clients/messages/
// publish/stream/goals/artifacts) is gone (404). The browser-side flow over the
// WebSocket is proven separately by the SDK integration suite + the headless
// agent-browser drive; this enforces the Go-side contract in CI.
func TestDashServeMintsBrowserSession(t *testing.T) {
	h := newHarness(t)

	// Enable the bus WebSocket listener via the config file (the brew-services path
	// the dash relies on) before the bus comes up, on a free loopback port. The dash
	// reads the resulting ws URL from the discovery file.
	wsAddr := freeLoopback(t)
	if _, code := h.run(nil, "config", "set", "--store", h.store, "ws-listen", wsAddr); code != 0 {
		t.Fatalf("config set ws-listen exited %d", code)
	}
	h.startBus()

	dashBin := buildDashBinary(t)
	dash := h.startBgBin(dashBin, nil, "--store", h.store, "--port", "0")
	urlLine := dash.waitStdout(t, "token=")
	m := regexp.MustCompile(`(http://127\.0\.0\.1:\d+)/\?token=(\S+)`).FindStringSubmatch(urlLine)
	if m == nil {
		t.Fatalf("no serve URL in stdout line %q", urlLine)
	}
	base, token := m[1], m[2]

	// --- POST /api/session: mint a browser credential ------------------------
	// Loopback is token-free (ADR-0032 exception, TASK-115); the dash listens on
	// 127.0.0.1, so this e2e is a loopback peer. The token still gates non-loopback
	// peers (not testable here — the listener is loopback-bound); the token-bearing
	// path is exercised below.
	var sess struct {
		ID    string `json:"id"`
		Creds string `json:"creds"`
		WSURL string `json:"wsURL"`
	}
	apiPostInto(t, base+"/api/session", token, &sess)
	if sess.ID == "" {
		t.Fatal("/api/session returned an empty minted id")
	}
	if !strings.Contains(sess.Creds, "NATS USER JWT") {
		t.Fatalf("/api/session creds is not a NATS credential: %q", sess.Creds)
	}
	if sess.WSURL != "ws://"+wsAddr {
		t.Fatalf("/api/session wsURL = %q, want ws://%s", sess.WSURL, wsAddr)
	}

	// Each tab mints a FRESH credential for the operator's OWN identity (ADR-0044):
	// a new keypair per tab (distinct creds), but the SAME id — the operator's, never
	// a per-tab child identity (the rc.1 bug that fix corrected).
	var sess2 struct {
		ID    string `json:"id"`
		Creds string `json:"creds"`
	}
	apiPostInto(t, base+"/api/session", token, &sess2)
	if sess2.Creds == sess.Creds {
		t.Fatalf("two /api/session calls returned the same creds — each tab must be minted a fresh keypair")
	}
	if sess2.ID != sess.ID {
		t.Fatalf("/api/session ids differ (%q vs %q) — every tab must be the operator's own identity, not a per-tab child", sess.ID, sess2.ID)
	}

	// --- the Go relay is gone: the old /api/* endpoints 404 (ADR-0044) -------
	for _, p := range []string{"/api/self", "/api/clients", "/api/goals", "/api/artifacts", "/api/subjects", "/api/messages?subject=msg.topic.x"} {
		if code := getStatus(t, base+p); code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404 (the relay is deleted)", p, code)
		}
	}

	// --- the survivors still serve ------------------------------------------
	for _, p := range []string{"/", "/debug"} {
		if code := getStatus(t, base+p); code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (static SPA host)", p, code)
		}
	}
}

func freeLoopback(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe a free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// apiPostInto POSTs (no body) to a token-gated endpoint and decodes the JSON reply.
func apiPostInto(t *testing.T, url, token string, v any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("POST %s = %d: %s", url, resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
}

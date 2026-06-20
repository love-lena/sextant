package dashapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
)

// browserKind is the kind the dash mints a browser tab as (ADR-0044) — the literal
// the bus bounds with a TTL. Asserted here rather than imported from wireapi: the
// dash is a client and must not reach the wire atom (the TestAppsNoWireAtom bright
// line), so the test pins the same literal the handler declares.
const browserKind = "browser"

// sessionBody decodes a POST /api/session response.
type sessionBody struct {
	ID    string `json:"id"`
	Creds string `json:"creds"`
	WSURL string `json:"wsURL"`
}

func postSession(srv http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/session", nil)
	req.RemoteAddr = "127.0.0.1:5000" // loopback — the dash listener's posture
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

// TestSessionMintsBrowserCredential is the core of the mint endpoint (ADR-0044):
// it dispatches clients.register for a kind="browser" child (so the bus bounds its
// JWT) and hands the page the minted creds + the bus WebSocket URL to dial.
func TestSessionMintsBrowserCredential(t *testing.T) {
	bus := &fakeBus{id: "01DASH"}
	srv := dashapi.New(dashapi.Config{Bus: bus, Token: "tok", WSURL: "ws://127.0.0.1:7423"})

	rec := postSession(srv)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var body sessionBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v (%s)", err, rec.Body.String())
	}
	if body.WSURL != "ws://127.0.0.1:7423" {
		t.Errorf("wsURL = %q, want the bus ws URL", body.WSURL)
	}
	if body.ID == "" || body.Creds == "" {
		t.Errorf("response missing minted id/creds: %+v", body)
	}

	// The child must be minted with kind="browser" — that is what makes the bus
	// bound its credential's TTL (the dash cannot retire it).
	calls := bus.registerCalls()
	if len(calls) != 1 {
		t.Fatalf("Register called %d times, want 1", len(calls))
	}
	if calls[0].kind != browserKind {
		t.Errorf("minted kind = %q, want %q", calls[0].kind, browserKind)
	}
}

// TestSessionDistinctPerTab: each POST /api/session mints a fresh credential, so
// two tabs never share an identity.
func TestSessionDistinctPerTab(t *testing.T) {
	bus := &fakeBus{id: "01DASH"}
	srv := dashapi.New(dashapi.Config{Bus: bus, Token: "tok", WSURL: "ws://127.0.0.1:7423"})

	var a, b sessionBody
	_ = json.Unmarshal(postSession(srv).Body.Bytes(), &a)
	_ = json.Unmarshal(postSession(srv).Body.Bytes(), &b)
	if a.Creds == b.Creds {
		t.Errorf("two tabs got the same creds %q — each must be minted fresh", a.Creds)
	}
	if len(bus.registerCalls()) != 2 {
		t.Errorf("Register called %d times, want 2 (one per tab)", len(bus.registerCalls()))
	}
}

// TestSessionFailsLoudWithoutWSListener: with no bus WebSocket listener configured
// the page has nowhere to connect, so the endpoint returns 503 with the exact
// remediation rather than handing back an unusable session.
func TestSessionFailsLoudWithoutWSListener(t *testing.T) {
	srv := dashapi.New(dashapi.Config{Bus: &fakeBus{id: "01DASH"}, Token: "tok", WSURL: ""})
	rec := postSession(srv)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (no ws listener)", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "ws-listen") {
		t.Errorf("503 body does not name the remediation (ws-listen): %s", body)
	}
}

// TestSessionMintFailureIsBadGateway: a bus that refuses the mint surfaces as 502.
func TestSessionMintFailureIsBadGateway(t *testing.T) {
	bus := &fakeBus{id: "01DASH", registerErr: &fakeError{"caller may not dispatch"}}
	srv := dashapi.New(dashapi.Config{Bus: bus, Token: "tok", WSURL: "ws://127.0.0.1:7423"})
	rec := postSession(srv)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (mint refused)", rec.Code)
	}
}

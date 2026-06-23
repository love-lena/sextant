package dashapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/love-lena/sextant/clients/go/apps/internal/dashapi"
)

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

// TestSessionMintsOperatorCredential is the core of the mint endpoint (ADR-0044):
// it dispatches clients.session over the dash's own connection and hands the page
// a credential for the dash's OWN identity (the operator's) plus the bus WebSocket
// URL to dial. The page connects with it and so acts AS the operator — the fix for
// the per-tab child id that broke DMs / self-authorship.
func TestSessionMintsOperatorCredential(t *testing.T) {
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
	if body.Creds == "" {
		t.Errorf("response missing minted creds: %+v", body)
	}
	// The session credential is for the dash's OWN id — that is what makes the page
	// act as the operator (same author, same DM/inbox space).
	if body.ID != "01DASH" {
		t.Errorf("session id = %q, want the dash's own id 01DASH", body.ID)
	}
	if bus.mintCount() != 1 {
		t.Fatalf("MintSession called %d times, want 1", bus.mintCount())
	}
}

// TestSessionDistinctPerTab: each POST /api/session mints a fresh credential, so
// two tabs never share credential material (even though both act as the operator).
func TestSessionDistinctPerTab(t *testing.T) {
	bus := &fakeBus{id: "01DASH"}
	srv := dashapi.New(dashapi.Config{Bus: bus, Token: "tok", WSURL: "ws://127.0.0.1:7423"})

	var a, b sessionBody
	_ = json.Unmarshal(postSession(srv).Body.Bytes(), &a)
	_ = json.Unmarshal(postSession(srv).Body.Bytes(), &b)
	if a.Creds == b.Creds {
		t.Errorf("two tabs got the same creds %q — each must be minted fresh", a.Creds)
	}
	if bus.mintCount() != 2 {
		t.Errorf("MintSession called %d times, want 2 (one per tab)", bus.mintCount())
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
	bus := &fakeBus{id: "01DASH", mintErr: &fakeError{"mint refused"}}
	srv := dashapi.New(dashapi.Config{Bus: bus, Token: "tok", WSURL: "ws://127.0.0.1:7423"})
	rec := postSession(srv)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (mint refused)", rec.Code)
	}
}

// Package dashapi is the dash's local HTTP face, reduced to what a browser that
// is itself a co-equal bus client needs (ADR-0044). The Go process no longer
// relays or re-implements any bus primitive: the browser connects to the bus
// directly over the WebSocket listener with @sextant/sdk and runs the goals/review
// conventions itself. So this server does exactly two things:
//
//   - serve the static SPA (GET / and /debug, plus the token-free /build.json
//     staleness asset) — the page the operator opens; and
//   - mint a short-lived, scoped browser credential (POST /api/session) — the one
//     thing a browser cannot do for itself, because minting stays at the bus and
//     the dash is the top-level client that may dispatch (mint-on-behalf, ADR-0033).
//
// This reverses ADR-0032's "the browser never touches the bus / the credential
// never leaves the process": a short-lived browser-scoped credential model now
// exists (ADR-0033 mint-on-behalf + a JWT TTL), so the credential does reach the
// page — over the dash's loopback, token-gated HTTPS — and the browser is a
// first-class client. The convention logic (the proof-filter, the review
// read-merge-CAS) lives in the TS conventions the browser runs, not here.
//
// The server depends only on Bus — the narrow subset of *sextant.Client it now
// needs (ID + Register) — so the mint handler is exercised against a fake.
package dashapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/love-lena/sextant/clients/go/sdk"
)

// Bus is the subset of *sextant.Client the API server needs (ADR-0044): the one
// bus act a browser cannot do for itself — MintSession mints a short-lived SESSION
// credential for the dash's OWN identity (the operator's), the credential the page
// connects with so it acts AS the operator (its DMs, its DM history, its
// authorship). *sextant.Client satisfies it; tests supply a fake. Everything the
// old API relayed (clients/messages/artifacts/publish/subscribe) is gone — the
// browser calls those over its own bus Client.
type Bus interface {
	MintSession(ctx context.Context) (sextant.IssuedClient, error)
}

// Config configures a Server.
type Config struct {
	// Bus is the connected bus client the mint endpoint dispatches through. Required.
	Bus Bus
	// Token is the per-launch secret a non-loopback /api request must present
	// (Bearer header or ?token=). Required; an empty token rejects all non-loopback
	// /api requests.
	Token string
	// WSURL is the bus WebSocket URL the minted browser dials (ws://host:port,
	// ADR-0044). Required for the mint endpoint to hand the page a usable session;
	// empty means the bus has no WebSocket listener and the handler fails loud with
	// the remediation.
	WSURL string
	// AllowedOrigins are extra browser origins permitted beyond localhost
	// (127.0.0.1 and localhost on any port are always allowed). Used for a separate
	// dev server hosting the UI.
	AllowedOrigins []string
	// UIDir, when set, serves a custom frontend directory at / instead of the
	// built-in designed UI (the dev hot-reload hook).
	UIDir string
}

// Server is the local static-SPA host + credential-mint endpoint. It implements
// http.Handler.
type Server struct {
	bus            Bus
	token          string
	wsURL          string
	allowedOrigins []string
	uiDir          string
	mux            *http.ServeMux
}

// New builds a Server from cfg. The returned Server is an http.Handler ready to
// pass to http.Serve.
func New(cfg Config) *Server {
	s := &Server{
		bus:            cfg.Bus,
		token:          cfg.Token,
		wsURL:          cfg.WSURL,
		allowedOrigins: cfg.AllowedOrigins,
		uiDir:          cfg.UIDir,
		mux:            http.NewServeMux(),
	}
	s.routes()
	return s
}

// routes registers the surviving routes (ADR-0044): the static SPA (/ and /debug,
// plus /build.json served as a static asset by handleRoot's file server) and the
// single token-gated mint endpoint. No /api/* relay, no SSE, no convention
// projection — the browser does all of that over its own bus Client.
func (s *Server) routes() {
	s.mux.HandleFunc("POST /api/session", s.gate(s.handleSession))
	s.mux.HandleFunc("GET /debug", s.handleDebug)
	s.mux.HandleFunc("GET /", s.handleRoot)
}

// ServeHTTP applies the origin guard to /api requests, then dispatches to the
// registered routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !s.applyCORS(w, r) {
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// applyCORS enforces the allowed-origin policy for a browser request and answers
// CORS preflight. It returns false when it has already written the response
// (a rejected origin, or a handled OPTIONS preflight) and the caller must stop.
// A request with no Origin header (curl, a same-origin simple GET) is not
// origin-gated — only the token gate applies.
func (s *Server) applyCORS(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if !s.originAllowed(origin) {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	return true
}

// originAllowed reports whether a browser Origin may call the API: any localhost
// origin (127.0.0.1, ::1, or the hostname "localhost", on any port) is always
// allowed — that is the same-origin page and a local dev server — plus any origin
// in the configured allow-list (exact match). A malformed origin is rejected.
func (s *Server) originAllowed(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	for _, allowed := range s.allowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

// gate wraps an /api handler with the access check (see authorized): a loopback
// peer is allowed without a token (ADR-0032 loopback exception, TASK-115); any
// non-loopback peer must present the per-launch token as `Authorization: Bearer
// <token>` or `?token=<token>`, or it is rejected 401.
func (s *Server) gate(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeError(w, http.StatusUnauthorized, "missing or invalid token")
			return
		}
		h(w, r)
	}
}

// authorized reports whether r may access the API. A loopback peer (127.0.0.0/8
// or ::1) is authorized WITHOUT a token: the dash listener is loopback-bound and
// loopback is host-bound + implicitly trusted (standard localhost practice, cf.
// OAuth's native-app loopback redirect), so the token's CSRF/remote barrier adds
// nothing there (ADR-0032 loopback exception, TASK-115). Any non-loopback peer
// must carry the valid per-launch token, as an `Authorization: Bearer <token>`
// header or a `?token=` query value; an empty server token is never authorized.
// The comparison is constant-time so a token can't be recovered by timing.
func (s *Server) authorized(r *http.Request) bool {
	if isLoopback(r.RemoteAddr) {
		return true
	}
	if s.token == "" {
		return false
	}
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && tokenEqual(bearer, s.token) {
		return true
	}
	return tokenEqual(r.URL.Query().Get("token"), s.token)
}

// isLoopback reports whether a request's peer address is a loopback IP
// (127.0.0.0/8 or ::1). A malformed/empty address is treated as non-loopback, so
// an unparseable peer falls through to the token check (deny-by-default).
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tokenEqual compares two tokens in constant time (subtle.ConstantTimeCompare
// returns 0 for unequal lengths, which only reveals length — fine for a
// fixed-size token).
func tokenEqual(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// sessionResponse is the JSON POST /api/session hands the page: the minted
// browser credential (the .creds text — bus auth material, the JWT+seed) and the
// bus WebSocket URL to dial. The page calls browserConnect({url, credsText}) with
// these (ADR-0044). The id is informational (the page reads its own id from the
// credential's JWT).
type sessionResponse struct {
	ID    string `json:"id"`
	Creds string `json:"creds"`
	WSURL string `json:"wsURL"`
}

// handleSession mints a short-lived SESSION credential for one dash tab (ADR-0044)
// — the reason a Go server still runs. It dispatches clients.session over the
// dash's own connection: the bus issues a fresh ephemeral keypair whose JWT name
// is the dash's OWN id (the operator's), so the credential authenticates AS the
// operator — its DMs, its DM history, its authorship, its presence — which a fresh
// per-tab child id silently broke (the credential could not read the operator's
// traffic and authored as a stranger). It stays browser-safe by the same mechanism
// a browser child did: the bus bounds its JWT with a short exp the dash cannot
// retire, and denies it the privileged issuance ops (it still cannot mint or
// retire). The .creds text rides this token-gated loopback response to the page,
// which then opens its own WebSocket to the bus.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if s.wsURL == "" {
		// The bus has no WebSocket listener — the browser has nowhere to connect.
		// Fail loud with the exact remediation (fail-loud discipline, ADR-0044).
		writeError(w, http.StatusServiceUnavailable,
			"the bus has no WebSocket listener; enable it with `sextant config set ws-listen 127.0.0.1:<port>` then restart the bus")
		return
	}
	issued, err := s.bus.MintSession(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "mint browser session credential: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse{ID: issued.ID, Creds: issued.Creds, WSURL: s.wsURL})
}

// writeJSON writes v as an indented JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// writeError writes a JSON error envelope: {"error": msg}.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
